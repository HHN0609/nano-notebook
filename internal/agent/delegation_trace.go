package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs/semconv"
	"github.com/jackc/pgx/v5"
)

func RecordDelegationCreatedInTx(ctx context.Context, tx pgx.Tx, parentRunID, childRunID string) error {
	if tx == nil || parentRunID == "" || childRunID == "" {
		return errors.New("delegation creation Trace is incomplete")
	}
	parentRecorder, parentTracer, err := rootTrace(ctx, tx, parentRunID)
	if err != nil {
		return err
	}
	childRecorder, childTracer, err := rootTrace(ctx, tx, childRunID)
	if err != nil {
		return err
	}
	parentContext := agentobs.ContextWithSpanContext(ctx, parentRecorder.RootSpanContext())
	childContext := agentobs.ContextWithSpanContext(ctx, childRecorder.RootSpanContext())
	if err := parentTracer.Link(parentContext, agentobs.Link{
		IdentityKey: "run/" + parentRunID + "/delegates/" + childRunID,
		Name:        semconv.LinkDelegates, Target: childRecorder.RootSpanContext(),
	}); err != nil {
		return err
	}
	if err := childTracer.Link(childContext, agentobs.Link{
		IdentityKey: "run/" + childRunID + "/delegated-from/" + parentRunID,
		Name:        semconv.LinkDelegatedFrom, Target: parentRecorder.RootSpanContext(),
	}); err != nil {
		return err
	}
	attributes := delegationTraceAttributes(parentRunID, childRunID, DelegationWaiting, false)
	return parentTracer.Event(parentContext, agentobs.Event{
		IdentityKey: "run/" + parentRunID + "/delegation/created",
		Name:        TraceEventDelegationCreated, Attributes: attributes,
	})
}

func RecordDelegationTerminalInTx(ctx context.Context, tx pgx.Tx, parentRunID, childRunID string, state DelegationState, errorCode string, parentWoken bool) error {
	if tx == nil || parentRunID == "" || childRunID == "" || state == DelegationWaiting {
		return errors.New("delegation terminal Trace is incomplete")
	}
	attributes := delegationTraceAttributes(parentRunID, childRunID, state, parentWoken)
	if errorCode != "" {
		attributes = append(attributes, agentobs.String(TraceKeyErrorCode, errorCode))
	}
	if err := recordRootEvent(ctx, tx, childRunID, agentobs.Event{
		IdentityKey: fmt.Sprintf("run/%s/delegation/%s", childRunID, state),
		Name:        TraceEventDelegationTerminal, Attributes: attributes,
	}); err != nil {
		return err
	}
	return recordRootEvent(ctx, tx, parentRunID, agentobs.Event{
		IdentityKey: fmt.Sprintf("run/%s/delegation/wake/%s", parentRunID, state),
		Name:        TraceEventDelegationWake, Attributes: attributes,
	})
}

func RecordDelegationConsumedInTx(ctx context.Context, tx pgx.Tx, parentRunID, childRunID string, terminal DelegationState) error {
	return recordRootEvent(ctx, tx, parentRunID, agentobs.Event{
		IdentityKey: fmt.Sprintf("run/%s/delegation/consumed/%s", parentRunID, terminal),
		Name:        TraceEventDelegationConsumed,
		Attributes:  delegationTraceAttributes(parentRunID, childRunID, terminal, true),
	})
}

func delegationTraceAttributes(parentRunID, childRunID string, state DelegationState, parentWoken bool) []agentobs.Attribute {
	return []agentobs.Attribute{
		agentobs.String(TraceKeyParentRunID, parentRunID),
		agentobs.String(TraceKeyChildRunID, childRunID),
		agentobs.String(TraceKeyDelegationState, string(state)),
		agentobs.Int64(TraceKeyDelegationOrdinal, 0),
		agentobs.Int64(TraceKeyDelegationDepth, 1),
		agentobs.Bool(TraceKeyParentWoken, parentWoken),
	}
}

func recordRootEvent(ctx context.Context, tx pgx.Tx, runID string, event agentobs.Event) error {
	recorder, tracer, err := rootTrace(ctx, tx, runID)
	if err != nil {
		return err
	}
	return tracer.Event(agentobs.ContextWithSpanContext(ctx, recorder.RootSpanContext()), event)
}

func rootTrace(ctx context.Context, tx pgx.Tx, runID string) (*RunTraceRecorder, *agentobs.Tracer, error) {
	var databaseRole string
	if err := tx.QueryRow(ctx, `select current_user`).Scan(&databaseRole); err != nil {
		return nil, nil, err
	}
	var recorder *RunTraceRecorder
	var err error
	if databaseRole == "nano_app" {
		recorder, err = NewOwnedRunTraceRecorder(ctx, tx, runID)
	} else {
		recorder, err = NewRunTraceRecorder(ctx, tx, runID)
	}
	if err != nil {
		return nil, nil, err
	}
	tracer, err := agentobs.NewTracer(agentobs.TracerConfig{Recorder: recorder, SemanticConventionVersion: TraceSemanticConventionVersion})
	if err != nil {
		return nil, nil, err
	}
	return recorder, tracer, nil
}
