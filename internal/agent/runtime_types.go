package agent

import (
	"context"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type Execution struct {
	Attempt
	ChatID                 string
	UserID                 string
	InputMessageID         string
	Model                  string
	ModelInvocation        models.ModelInvocationPolicy
	PromptVersion          string
	AgentConfigID          string
	TimeZone               string
	DeadlineAt             time.Time
	ActionDecisionLimit    int
	FinalDecisionLimit     int
	ActionLimit            int
	ActionBatchLimit       int
	ActionResultByteLimit  int
	ActionResultsByteLimit int
	SelectedSourceCount    int
	MemberRole             string
	ExistingChildCount     int
}

type Attempt struct {
	JobID      string
	RunID      string
	AttemptNo  int
	LeaseToken string
}

func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}
