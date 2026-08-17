package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

var ErrContextBudgetExceeded = errors.New("context_budget_exhausted")

const (
	CompactionTriggerThreshold        = "threshold"
	CompactionTriggerProviderOverflow = "provider_overflow"
)

const ContextUnitCompactionSummary ContextUnitKind = "compaction_summary"

type ContextCompaction struct {
	ID                string
	PredecessorID     string
	Summary           string
	SummarizedThrough string
	SuffixStart       string
	TriggerReason     string
	BeforeTokens      int
	AfterTokens       int
}

func ContextUnitKey(unit ContextUnit) string {
	switch unit.Kind {
	case ContextUnitUserMessage:
		return "message:" + unit.MessageID
	case ContextUnitAgentStep:
		return fmt.Sprintf("run:%s/decision:%d", unit.RunID, unit.DecisionNo)
	default:
		return ""
	}
}

// SelectCompactionCut walks complete Context Units from the tail. If the
// retention target lands inside an Agent Step, the entire Step remains exact.
func SelectCompactionCut(units []ContextUnit, keepRecentTokens int) (int, error) {
	if len(units) < 2 || keepRecentTokens < 1 {
		return 0, ErrContextBudgetExceeded
	}
	tokens := 0
	cut := len(units)
	for index := len(units) - 1; index >= 0; index-- {
		unitTokens, err := estimateContextUnitTokens(units[index])
		if err != nil {
			return 0, err
		}
		if tokens > 0 && tokens+unitTokens > keepRecentTokens {
			break
		}
		tokens += unitTokens
		cut = index
		if tokens >= keepRecentTokens {
			break
		}
	}
	if cut <= 0 || cut >= len(units) {
		return 0, ErrContextBudgetExceeded
	}
	return cut, nil
}

func ApplyContextCompaction(units []ContextUnit, compaction ContextCompaction) ([]ContextUnit, error) {
	if strings.TrimSpace(compaction.Summary) == "" || compaction.SuffixStart == "" || compaction.SummarizedThrough == "" {
		return nil, projectionError("invalid Compaction boundary")
	}
	suffixIndex := -1
	summaryIndex := -1
	for index, unit := range units {
		key := ContextUnitKey(unit)
		if key == compaction.SuffixStart {
			suffixIndex = index
		}
		if key == compaction.SummarizedThrough {
			summaryIndex = index
		}
	}
	if suffixIndex < 1 || summaryIndex != suffixIndex-1 {
		return nil, projectionError("Compaction boundary is not present in the Chat lane")
	}
	summary := strings.ReplaceAll(strings.TrimSpace(compaction.Summary), "</summary>", "&lt;/summary&gt;")
	projected := make([]ContextUnit, 0, 1+len(units)-suffixIndex)
	projected = append(projected, ContextUnit{
		Kind:     ContextUnitCompactionSummary,
		Messages: []models.ModelMessage{{Role: models.RoleUser, Content: "<summary>" + summary + "</summary>"}},
	})
	projected = append(projected, units[suffixIndex:]...)
	return projected, nil
}

func estimateContextUnitTokens(unit ContextUnit) (int, error) {
	encoded, err := json.Marshal(unit.Messages)
	if err != nil {
		return 0, err
	}
	return maxInt(1, (utf8.RuneCount(encoded)+3)/4), nil
}
