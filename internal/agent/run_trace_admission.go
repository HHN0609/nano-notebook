package agent

import (
	"context"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs/semconv"
	"github.com/jackc/pgx/v5"
)

func StartRunTraceInTx(ctx context.Context, tx pgx.Tx, runID, model, promptVersion string, retryFrom *agentobs.SpanContext) error {
	if tx == nil || runID == "" || model == "" || promptVersion == "" {
		return errors.New("Run Trace admission is incomplete")
	}
	recorder, err := NewAdmissionTraceRecorder(tx, runID)
	if err != nil {
		return err
	}
	tracer, err := agentobs.NewTracer(agentobs.TracerConfig{
		Recorder: recorder, SemanticConventionVersion: TraceSemanticConventionVersion,
	})
	if err != nil {
		return err
	}
	var role AgentRole
	var configurationSetID, executorVersion string
	if err := tx.QueryRow(ctx, `
		select agent_role,agent_config_id,executor_version from agent_runs where id=$1
	`, runID).Scan(&role, &configurationSetID, &executorVersion); err != nil {
		return err
	}
	attributes := []agentobs.Attribute{
		agentobs.String(TraceKeyRunID, runID),
		agentobs.String(TraceKeyRunModel, model),
		agentobs.String(TraceKeyPromptVersion, promptVersion),
		agentobs.String(TraceKeyAgentRole, string(role)),
		agentobs.String(TraceKeyConfigurationSetID, configurationSetID),
		agentobs.String(TraceKeyExecutorVersion, executorVersion),
	}
	rootContext, _, err := tracer.StartTrace(ctx, agentobs.TraceStart{
		IdentityKey: "run/" + runID + "/root/start",
		Name:        TraceSpanAgentExecution,
		Attributes:  attributes,
	})
	if err != nil {
		return err
	}
	if err := tracer.Event(rootContext, agentobs.Event{
		IdentityKey: "run/" + runID + "/admitted",
		Name:        TraceEventRunAdmitted,
		Attributes: []agentobs.Attribute{
			agentobs.String(TraceKeyRunID, runID),
			agentobs.String(TraceKeyRunStatus, "queued"),
		},
	}); err != nil {
		return err
	}
	if retryFrom == nil {
		return nil
	}
	if err := retryFrom.Validate(); err != nil {
		return err
	}
	if err := tracer.Link(rootContext, agentobs.Link{
		IdentityKey: "run/" + runID + "/retried-from",
		Name:        semconv.LinkRetriedFrom,
		Target:      *retryFrom,
	}); err != nil {
		return err
	}
	return tracer.Event(rootContext, agentobs.Event{
		IdentityKey: "run/" + runID + "/retry-admitted",
		Name:        TraceEventRetryAdmitted,
		Attributes:  []agentobs.Attribute{agentobs.String(TraceKeyRunID, runID)},
	})
}
