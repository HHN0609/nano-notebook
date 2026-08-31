package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type ActionResultStatus string

const (
	ActionSucceeded   ActionResultStatus = "succeeded"
	ActionDomainError ActionResultStatus = "domain_error"
)

type ActionRequest struct {
	ActionID         string
	Input            json.RawMessage
	DefaultTimeZone  string
	Attempt          Attempt
	Definition       agentcatalog.Reference
	DefinitionSHA256 string
}

type ActionResult struct {
	Status          ActionResultStatus
	Output          json.RawMessage
	ErrorCode       string
	Error           *ActionError
	traceAttributes []agentobs.Attribute
}

type ActionError struct {
	Kind       string `json:"kind"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion,omitempty"`
	Retryable  bool   `json:"retryable"`
}

func (e ActionError) Validate() error {
	if !actionCodePattern.MatchString(e.Kind) || !actionCodePattern.MatchString(e.Code) ||
		strings.TrimSpace(e.Message) == "" || len([]rune(e.Message)) > 512 ||
		len([]rune(e.Suggestion)) > 512 {
		return fmt.Errorf("invalid structured Action error")
	}
	return nil
}

func (r ActionResult) Validate() error {
	switch r.Status {
	case ActionSucceeded:
		var output map[string]json.RawMessage
		if r.ErrorCode != "" || r.Error != nil || len(r.Output) == 0 || json.Unmarshal(r.Output, &output) != nil || output == nil {
			return fmt.Errorf("invalid succeeded Action result")
		}
	case ActionDomainError:
		legacy := r.Error == nil && actionCodePattern.MatchString(r.ErrorCode)
		structured := r.ErrorCode == "" && r.Error != nil && r.Error.Validate() == nil
		if len(r.Output) != 0 || (!legacy && !structured) {
			return fmt.Errorf("invalid domain-error Action result")
		}
	default:
		return fmt.Errorf("unknown Action result status %q", r.Status)
	}
	return nil
}

type Action interface {
	Definition() models.ActionDefinition
	ValidateInput(json.RawMessage) error
	Execute(context.Context, ActionRequest) (ActionResult, error)
}

type ActionPolicy struct {
	AllowedNames           map[string]bool
	RemainingActions       int
	RemainingPlanMutations int
	Execution              *Execution
}

type ActionAvailability interface {
	Available(Execution) (bool, string)
}

type ActionRegistry struct {
	actions []registeredAction
	byName  map[string]Action
}

type registeredAction struct {
	definition models.ActionDefinition
	executor   Action
}

var actionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
var actionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func NewActionRegistry(actions ...Action) (*ActionRegistry, error) {
	registered := make([]registeredAction, 0, len(actions))
	for _, action := range actions {
		definition := action.Definition()
		var schema map[string]json.RawMessage
		if !actionNamePattern.MatchString(definition.Name) {
			return nil, fmt.Errorf("invalid Action name %q", definition.Name)
		}
		if description := strings.TrimSpace(definition.Description); description == "" || len([]rune(description)) > 512 {
			return nil, fmt.Errorf("invalid Action description for %q", definition.Name)
		}
		if len(definition.InputSchema) == 0 || len(definition.InputSchema) > 16*1024 || json.Unmarshal(definition.InputSchema, &schema) != nil || schema == nil {
			return nil, fmt.Errorf("invalid Action schema for %q", definition.Name)
		}
		definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
		registered = append(registered, registeredAction{definition: definition, executor: action})
	}
	sort.Slice(registered, func(i, j int) bool {
		return registered[i].definition.Name < registered[j].definition.Name
	})
	byName := make(map[string]Action, len(registered))
	for _, item := range registered {
		name := item.definition.Name
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("duplicate Action name %q", name)
		}
		byName[name] = item.executor
	}
	return &ActionRegistry{actions: registered, byName: byName}, nil
}

func (r *ActionRegistry) Definitions(ctx context.Context, policy ActionPolicy, tracers ...*agentobs.Tracer) []models.ActionDefinition {
	if policy.RemainingActions <= 0 && policy.RemainingPlanMutations <= 0 {
		return nil
	}
	var tracer *agentobs.Tracer
	if len(tracers) > 0 {
		tracer = tracers[0]
	}
	definitions := make([]models.ActionDefinition, 0, len(r.actions))
	for _, action := range r.actions {
		definition := action.definition
		planMutation := false
		if mutation, ok := action.executor.(PlanMutationPolicy); ok {
			planMutation = mutation.IsPlanMutation()
		}
		if (planMutation && policy.RemainingPlanMutations <= 0) || (!planMutation && policy.RemainingActions <= 0) {
			continue
		}
		if policy.Execution != nil {
			if availability, ok := action.executor.(ActionAvailability); ok {
				available, reasonCode := availability.Available(*policy.Execution)
				if !available {
					recordActionAvailabilityFiltered(ctx, tracer, definition.Name, reasonCode)
					continue
				}
			}
		}
		if policy.AllowedNames != nil && !policy.AllowedNames[definition.Name] {
			continue
		}
		definition.InputSchema = append(json.RawMessage(nil), definition.InputSchema...)
		definitions = append(definitions, definition)
	}
	return definitions
}

func recordActionAvailabilityFiltered(ctx context.Context, tracer *agentobs.Tracer, toolName, reasonCode string) {
	if tracer == nil || reasonCode == "" || toolName == "" {
		return
	}
	span, ok := agentobs.SpanContextFromContext(ctx)
	if !ok {
		return
	}
	_ = tracer.Event(ctx, agentobs.Event{
		IdentityKey: fmt.Sprintf("tool/%s/filtered/%s/%s/%d", toolName, span.TraceID, span.SpanID, time.Now().UnixNano()),
		Name:        TraceEventToolFiltered,
		Attributes: []agentobs.Attribute{
			agentobs.String(TraceKeyToolName, toolName),
			agentobs.String(TraceKeyToolReasonCode, reasonCode),
		},
	})
}

func (r *ActionRegistry) Resolve(name string) (Action, bool) {
	action, ok := r.byName[name]
	return action, ok
}

func (r *ActionRegistry) ValidateProposal(actions []models.ActionProposal) error {
	if len(actions) == 0 {
		return fmt.Errorf("Action proposal batch is empty")
	}
	for index, proposal := range actions {
		action, ok := r.Resolve(proposal.Name)
		if !ok {
			return fmt.Errorf("Action proposal %d names unknown Action %q", index, proposal.Name)
		}
		if err := action.ValidateInput(proposal.Input); err != nil {
			return fmt.Errorf("Action proposal %d input is invalid: %w", index, err)
		}
	}
	return nil
}
