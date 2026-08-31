package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type ToolInputRepeat struct {
	Name           string
	CanonicalInput string
	Count          int
}

type AgentStatusObservation struct {
	GeneratedAt  time.Time
	TimeZone     string
	Todo         *TodoSnapshot
	ToolCalls    map[string]int
	ExactRepeats []ToolInputRepeat
	LatestError  *ActionError
}

func ObserveAgentStatus(inputMessageID string, prefixes []CheckpointPrefix, generatedAt time.Time, timeZone string) (AgentStatusObservation, error) {
	if strings.TrimSpace(inputMessageID) == "" || generatedAt.IsZero() {
		return AgentStatusObservation{}, errors.New("invalid Agent Status scope")
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		return AgentStatusObservation{}, fmt.Errorf("invalid Agent Status time zone: %w", err)
	}
	observation := AgentStatusObservation{
		GeneratedAt: generatedAt, TimeZone: timeZone, ToolCalls: make(map[string]int), ExactRepeats: make([]ToolInputRepeat, 0),
	}
	exact := make(map[string]int)
	type exactIdentity struct{ name, input string }
	identities := make(map[string]exactIdentity)
	for _, prefix := range prefixes {
		for _, proposal := range prefix.Proposals {
			for _, action := range proposal.Actions {
				observation.ToolCalls[action.Name]++
				canonical, err := CanonicalJSONObject(action.Input)
				if err != nil {
					return AgentStatusObservation{}, err
				}
				key := action.Name + "\x00" + string(canonical)
				exact[key]++
				identities[key] = exactIdentity{name: action.Name, input: string(canonical)}
				if action.Result == nil {
					continue
				}
				if action.Result.Status == ActionDomainError {
					if action.Result.Error != nil {
						detail := *action.Result.Error
						observation.LatestError = &detail
					} else if action.Result.ErrorCode != "" {
						observation.LatestError = &ActionError{Kind: "domain", Code: action.Result.ErrorCode, Message: "The tool returned a domain error."}
					}
					continue
				}
				if action.Name != "rewrite_todo_list" && action.Name != "update_todo_status" {
					continue
				}
				var snapshot TodoSnapshot
				decoder := json.NewDecoder(bytes.NewReader(action.Result.Output))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&snapshot); err != nil {
					return AgentStatusObservation{}, fmt.Errorf("invalid TODO snapshot: %w", err)
				}
				if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
					return AgentStatusObservation{}, errors.New("invalid TODO snapshot trailing JSON")
				}
				if err := validateTodoSnapshot(snapshot, inputMessageID); err != nil {
					return AgentStatusObservation{}, err
				}
				cloned := cloneTodoSnapshot(snapshot)
				observation.Todo = &cloned
			}
		}
	}
	for key, count := range exact {
		if count < 2 {
			continue
		}
		identity := identities[key]
		observation.ExactRepeats = append(observation.ExactRepeats, ToolInputRepeat{
			Name: identity.name, CanonicalInput: identity.input, Count: count,
		})
	}
	sort.Slice(observation.ExactRepeats, func(i, j int) bool {
		if observation.ExactRepeats[i].Name == observation.ExactRepeats[j].Name {
			return observation.ExactRepeats[i].CanonicalInput < observation.ExactRepeats[j].CanonicalInput
		}
		return observation.ExactRepeats[i].Name < observation.ExactRepeats[j].Name
	})
	return observation, nil
}

func RenderAgentStatus(observation AgentStatusObservation) (string, error) {
	location, err := time.LoadLocation(observation.TimeZone)
	if err != nil || observation.GeneratedAt.IsZero() {
		return "", errors.New("invalid Agent Status observation")
	}
	var builder strings.Builder
	builder.WriteString("<agent_status version=\"1\">\n")
	fmt.Fprintf(&builder, "Generated at: %s\n", observation.GeneratedAt.In(location).Format(time.RFC3339))
	fmt.Fprintf(&builder, "Time zone: %s\n", observation.TimeZone)
	if observation.Todo != nil {
		if err := validateTodoSnapshot(*observation.Todo, observation.Todo.InputMessageID); err != nil {
			return "", err
		}
		fmt.Fprintf(&builder, "\nTODO List (revision=%d):\n", observation.Todo.Revision)
		for _, item := range observation.Todo.Items {
			fmt.Fprintf(&builder, "- [%s] %s | %s | created_at=%s | updated_at=%s\n",
				item.ID, item.Status, escapeAgentStatusData(item.Content),
				item.CreatedAt.In(location).Format(time.RFC3339), item.UpdatedAt.In(location).Format(time.RFC3339))
		}
	}
	if len(observation.ToolCalls) > 0 || len(observation.ExactRepeats) > 0 {
		builder.WriteString("\nTool Calls:\n")
		names := make([]string, 0, len(observation.ToolCalls))
		for name := range observation.ToolCalls {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&builder, "- %s: %d\n", name, observation.ToolCalls[name])
		}
		for _, repeat := range observation.ExactRepeats {
			fmt.Fprintf(&builder, "- identical %s input repeated: %d\n", repeat.Name, repeat.Count)
		}
	}
	if observation.LatestError != nil {
		if err := observation.LatestError.Validate(); err != nil {
			// A historical v1 error has a generated safe message and an empty
			// suggestion, so it remains valid under the same boundary.
			return "", err
		}
		builder.WriteString("\nLatest Tool Error:\n")
		fmt.Fprintf(&builder, "- kind=%s code=%s retryable=%t | %s", observation.LatestError.Kind,
			observation.LatestError.Code, observation.LatestError.Retryable, escapeAgentStatusData(observation.LatestError.Message))
		if observation.LatestError.Suggestion != "" {
			fmt.Fprintf(&builder, " | suggestion=%s", escapeAgentStatusData(observation.LatestError.Suggestion))
		}
		builder.WriteByte('\n')
	}
	builder.WriteString("</agent_status>")
	return builder.String(), nil
}

func FinalizeDecisionRequest(request *models.ModelRequest, renderedStatus string) {
	if request == nil {
		return
	}
	request.Messages = append(request.Messages, models.ModelMessage{Role: models.RoleUser, Content: renderedStatus})
}

func attachAgentStatusTelemetry(request *models.ModelRequest, observation AgentStatusObservation, rendered string) {
	if request == nil {
		return
	}
	metadata := &request.ContextTelemetry
	metadata.AgentStatusInjected = true
	metadata.AgentStatusBytes = len([]byte(rendered))
	metadata.AgentStatusTokens = maxInt(1, (utf8.RuneCountInString(rendered)+3)/4)
	metadata.TodoRevision = 0
	metadata.TodoPendingCount = 0
	metadata.TodoInProgressCount = 0
	metadata.TodoCompletedCount = 0
	metadata.TodoCancelledCount = 0
	metadata.MaxToolInputRepeatCount = 0
	if observation.Todo != nil {
		metadata.TodoRevision = observation.Todo.Revision
		for _, item := range observation.Todo.Items {
			switch item.Status {
			case TodoPending:
				metadata.TodoPendingCount++
			case TodoInProgress:
				metadata.TodoInProgressCount++
			case TodoCompleted:
				metadata.TodoCompletedCount++
			case TodoCancelled:
				metadata.TodoCancelledCount++
			}
		}
	}
	for _, repeat := range observation.ExactRepeats {
		if repeat.Count > metadata.MaxToolInputRepeatCount {
			metadata.MaxToolInputRepeatCount = repeat.Count
		}
	}
}

func escapeAgentStatusData(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
