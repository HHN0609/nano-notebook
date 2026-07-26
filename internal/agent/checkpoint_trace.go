package agent

import (
	"context"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/jackc/pgx/v5"
)

func RecordCheckpointAcceptedInTx(ctx context.Context, tx pgx.Tx, attempt Attempt, checkpoint Checkpoint) error {
	recorder, err := NewRunTraceRecorder(ctx, tx, attempt.RunID)
	if err != nil {
		return err
	}
	attemptSpan, err := recorder.SpanContextByIdentity(ctx, TraceAttemptStartIdentity(attempt.RunID, attempt.AttemptNo))
	if err != nil {
		return err
	}
	tracer, err := agentobs.NewTracer(agentobs.TracerConfig{
		Recorder: recorder, SemanticConventionVersion: TraceSemanticConventionVersion,
	})
	if err != nil {
		return err
	}
	attributes := []agentobs.Attribute{
		agentobs.String(TraceKeyCheckpointKind, string(checkpoint.Kind)),
		agentobs.Int64(TraceKeyDecisionNumber, int64(checkpoint.DecisionNo)),
	}
	if checkpoint.ActionIndex != nil {
		attributes = append(attributes, agentobs.Int64(TraceKeyActionIndex, int64(*checkpoint.ActionIndex)))
	}
	return tracer.Event(agentobs.ContextWithSpanContext(ctx, attemptSpan), agentobs.Event{
		IdentityKey: fmt.Sprintf("run/%s/checkpoint/%s/accepted", attempt.RunID, checkpoint.IdentityKey),
		Name:        TraceEventCheckpointAccepted,
		OccurredAt:  checkpoint.CreatedAt,
		Attributes:  attributes,
	})
}

func RecordRoleCheckpointAcceptedInTx(ctx context.Context, tx pgx.Tx, attempt Attempt, checkpoint RoleCheckpoint) error {
	recorder, err := NewRunTraceRecorder(ctx, tx, attempt.RunID)
	if err != nil {
		return err
	}
	attemptSpan, err := recorder.SpanContextByIdentity(ctx, TraceAttemptStartIdentity(attempt.RunID, attempt.AttemptNo))
	if err != nil {
		return err
	}
	tracer, err := agentobs.NewTracer(agentobs.TracerConfig{Recorder: recorder, SemanticConventionVersion: TraceSemanticConventionVersion})
	if err != nil {
		return err
	}
	return tracer.Event(agentobs.ContextWithSpanContext(ctx, attemptSpan), agentobs.Event{
		IdentityKey: fmt.Sprintf("run/%s/checkpoint/%s/accepted", attempt.RunID, checkpoint.IdentityKey),
		Name:        TraceEventCheckpointAccepted,
		Attributes: []agentobs.Attribute{
			agentobs.String(TraceKeyAgentRole, string(checkpoint.Role)),
			agentobs.String(TraceKeyCheckpointStep, checkpoint.Step),
			agentobs.Int64(TraceKeyCheckpointOrdinal, int64(checkpoint.Ordinal)),
			agentobs.String(TraceKeyCheckpointPayloadSHA256, checkpoint.PayloadSHA256),
		},
	})
}
