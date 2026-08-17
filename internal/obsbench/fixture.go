package obsbench

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentbatch"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs/semconv"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
)

// RootFixture is one fully materialized synthetic root Agent Run and any child Runs.
type RootFixture struct {
	Identity       RootIdentity
	TotalAgentRuns int
	Envelopes      []agentbatch.Envelope
}

// CycleStats connects product demand to the execution and record pressure it creates.
type CycleStats struct {
	RootAgentRuns    int
	TotalAgentRuns   int
	Records          int
	ChildRunsPerRoot float64
	RecordsPerRoot   float64
}

// BuildRootFixture materializes the Durable Agent Trace facts for one reference root.
func (w Workload) BuildRootFixture(seed string, ordinal uint64, eventEpoch time.Time) (RootFixture, error) {
	if eventEpoch.IsZero() {
		return RootFixture{}, errors.New("observability benchmark event epoch is required")
	}
	identity, err := w.RootIdentity(seed, ordinal)
	if err != nil {
		return RootFixture{}, err
	}
	switch identity.Scenario {
	case ScenarioDirectAnswer:
		return buildSuccessfulFixture(identity, eventEpoch.UTC(), 0), nil
	case ScenarioSingleAction:
		return buildSuccessfulFixture(identity, eventEpoch.UTC(), 1), nil
	case ScenarioTwoActions:
		return buildSuccessfulFixture(identity, eventEpoch.UTC(), 2), nil
	case ScenarioDelegation:
		return buildDelegationFixture(identity, eventEpoch.UTC()), nil
	case ScenarioRetryRecovery:
		return buildRetryRecoveryFixture(identity, eventEpoch.UTC()), nil
	default:
		return RootFixture{}, fmt.Errorf("observability benchmark scenario %q is not materialized", identity.Scenario)
	}
}

// MaterializedCycleStats measures amplification from the actual fixture records.
func (w Workload) MaterializedCycleStats(seed string, eventEpoch time.Time) (CycleStats, error) {
	if w.CycleSize() == 0 {
		return CycleStats{}, errors.New("observability benchmark workload has no scenarios")
	}
	stats := CycleStats{RootAgentRuns: w.CycleSize()}
	for ordinal := 0; ordinal < w.CycleSize(); ordinal++ {
		fixture, err := w.BuildRootFixture(seed, uint64(ordinal), eventEpoch)
		if err != nil {
			return CycleStats{}, err
		}
		stats.TotalAgentRuns += fixture.TotalAgentRuns
		stats.Records += len(fixture.Envelopes)
	}
	stats.ChildRunsPerRoot = float64(stats.TotalAgentRuns-stats.RootAgentRuns) / float64(stats.RootAgentRuns)
	stats.RecordsPerRoot = float64(stats.Records) / float64(stats.RootAgentRuns)
	return stats, nil
}

func buildSuccessfulFixture(identity RootIdentity, eventEpoch time.Time, actionCount int) RootFixture {
	traceID := agentobs.TraceID(identity.TraceID)
	rootID := agentobs.SpanID("root-" + identity.TraceID[len("trace-"):])
	attemptID := agentobs.SpanID("attempt-1-" + identity.TraceID[len("trace-"):])
	publicationID := agentobs.SpanID("publication-" + identity.TraceID[len("trace-"):])
	descriptor := collector.TraceDescriptor{
		TraceID: traceID, WorkloadKind: collector.WorkloadAgentRun, WorkloadID: identity.RunID,
		RunID: identity.RunID, ChatID: identity.ChatID, NotebookID: identity.NotebookID,
		RootSpanID: rootID, AgentName: "nano-default-agent", SchemaVersion: 1, SemanticConventionVersion: 1,
	}
	base := eventEpoch.Add(time.Duration(identity.Ordinal) * time.Millisecond)
	records := make([]agentobs.Record, 0, 15+3*actionCount)
	appendRecord := func(kind agentobs.RecordKind, spanID agentobs.SpanID, identityKey, name string, attributes ...agentobs.Attribute) {
		records = append(records, agentobs.Record{
			SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
			IdentityKey: identityKey, Kind: kind, TraceID: traceID, SpanID: spanID, Name: name,
			OccurredAt: base.Add(time.Duration(len(records)) * time.Microsecond), Attributes: attributes,
		})
	}
	appendRecord(agentobs.RecordSpanStarted, rootID, "run/"+identity.RunID+"/root/start", "agent.execution")
	appendRecord(agentobs.RecordEvent, rootID, "run/"+identity.RunID+"/admitted", "nano.run.admitted",
		agentobs.String("nano.run.id", identity.RunID))
	appendRecord(agentobs.RecordSpanStarted, attemptID, "run/"+identity.RunID+"/attempt/1/start", "nano.job.attempt",
		agentobs.Int64("nano.attempt.number", 1))
	records[len(records)-1].ParentSpanID = rootID
	appendModelCall := func(number int, inputTokens, outputTokens int64) {
		modelID := agentobs.SpanID(fmt.Sprintf("model-%d-%s", number, identity.TraceID[len("trace-"):]))
		prefix := fmt.Sprintf("run/%s/attempt/1/model/%d", identity.RunID, number)
		appendRecord(agentobs.RecordSpanStarted, modelID, prefix+"/start", semconv.ModelCall,
			agentobs.String(semconv.ModelNameKey, "benchmark-model"))
		records[len(records)-1].ParentSpanID = attemptID
		appendRecord(agentobs.RecordSpanEnded, modelID, prefix+"/end", semconv.ModelCall,
			agentobs.String(semconv.ModelNameKey, "benchmark-model"),
			agentobs.Int64(semconv.TokenInputKey, inputTokens), agentobs.Int64(semconv.TokenOutputKey, outputTokens),
			agentobs.Int64(semconv.TokenTotalKey, inputTokens+outputTokens), agentobs.Bool(semconv.CostKnownKey, true),
			agentobs.Float64(semconv.CostAmountKey, float64(inputTokens+outputTokens)/384_000),
			agentobs.String(semconv.CostCurrencyKey, "USD"), agentobs.String(semconv.CostSourceKey, "benchmark"))
		records[len(records)-1].Status = agentobs.StatusOK
	}
	appendModelCall(1, 256, 128)
	if actionCount > 0 {
		appendRecord(agentobs.RecordEvent, attemptID, "run/"+identity.RunID+"/checkpoint/proposal/1", "nano.checkpoint.accepted",
			agentobs.String("nano.checkpoint.kind", "proposal"), agentobs.Int64("nano.checkpoint.ordinal", 1))
		for actionIndex := 1; actionIndex <= actionCount; actionIndex++ {
			actionID := agentobs.SpanID(fmt.Sprintf("action-%d-%s", actionIndex, identity.TraceID[len("trace-"):]))
			prefix := fmt.Sprintf("run/%s/attempt/1/action/%d", identity.RunID, actionIndex)
			appendRecord(agentobs.RecordSpanStarted, actionID, prefix+"/start", semconv.AgentAction,
				agentobs.String(semconv.ActionNameKey, "benchmark_action"), agentobs.Int64("nano.action.index", int64(actionIndex-1)))
			records[len(records)-1].ParentSpanID = attemptID
			appendRecord(agentobs.RecordSpanEnded, actionID, prefix+"/end", semconv.AgentAction,
				agentobs.String(semconv.ActionNameKey, "benchmark_action"))
			records[len(records)-1].Status = agentobs.StatusOK
			appendRecord(agentobs.RecordEvent, attemptID, prefix+"/checkpoint/result", "nano.checkpoint.accepted",
				agentobs.String("nano.checkpoint.kind", "action_result"), agentobs.Int64("nano.checkpoint.ordinal", int64(actionIndex+1)))
		}
		appendModelCall(2, 384, 160)
	}
	appendRecord(agentobs.RecordEvent, attemptID, "run/"+identity.RunID+"/checkpoint/final/1", "nano.checkpoint.accepted",
		agentobs.String("nano.checkpoint.kind", "final"), agentobs.Int64("nano.checkpoint.ordinal", int64(actionCount+2)))
	appendRecord(agentobs.RecordSpanStarted, publicationID, "run/"+identity.RunID+"/publication/start", "nano.publication")
	records[len(records)-1].ParentSpanID = rootID
	appendRecord(agentobs.RecordEvent, publicationID, "run/"+identity.RunID+"/publication/passed", "nano.publication.passed")
	appendRecord(agentobs.RecordSpanEnded, publicationID, "run/"+identity.RunID+"/publication/end", "nano.publication")
	records[len(records)-1].Status = agentobs.StatusOK
	appendRecord(agentobs.RecordSpanEnded, attemptID, "run/"+identity.RunID+"/attempt/1/end", "nano.job.attempt")
	records[len(records)-1].Status = agentobs.StatusOK
	appendRecord(agentobs.RecordEvent, rootID, "run/"+identity.RunID+"/terminal", "nano.run.terminal",
		agentobs.String("nano.run.status", "completed"))
	appendRecord(agentobs.RecordSpanEnded, rootID, "run/"+identity.RunID+"/root/end", "agent.execution")
	records[len(records)-1].Status = agentobs.StatusOK

	envelopes := make([]agentbatch.Envelope, len(records))
	for index, record := range records {
		envelopes[index] = agentbatch.Envelope{Trace: descriptor, Record: record}
	}
	return RootFixture{Identity: identity, TotalAgentRuns: 1, Envelopes: envelopes}
}

func buildDelegationFixture(identity RootIdentity, eventEpoch time.Time) RootFixture {
	parent := buildSuccessfulFixture(identity, eventEpoch, 1)
	childOpaque := sha256.Sum256([]byte(identity.TraceID + "\x00delegated-child"))
	childSuffix := hex.EncodeToString(childOpaque[:16])
	childIdentity := RootIdentity{
		Ordinal: identity.Ordinal, Scenario: ScenarioDirectAnswer,
		TraceID: "trace-" + childSuffix, RunID: "run-" + childSuffix,
		ChatID: identity.ChatID, NotebookID: identity.NotebookID,
	}
	child := buildSuccessfulFixture(childIdentity, eventEpoch.Add(50*time.Microsecond), 0)
	parentDescriptor := parent.Envelopes[0].Trace
	childDescriptor := child.Envelopes[0].Trace
	base := eventEpoch.Add(time.Duration(identity.Ordinal)*time.Millisecond + 100*time.Microsecond)
	attributes := []agentobs.Attribute{
		agentobs.String("nano.delegation.parent_run_id", identity.RunID),
		agentobs.String("nano.delegation.child_run_id", childIdentity.RunID),
		agentobs.String("nano.delegation.state", "completed"),
		agentobs.Int64("nano.delegation.ordinal", 0), agentobs.Int64("nano.delegation.depth", 1),
	}
	relationships := []agentbatch.Envelope{
		{Trace: parentDescriptor, Record: benchmarkLink(parentDescriptor.TraceID, parentDescriptor.RootSpanID,
			"run/"+identity.RunID+"/delegates/"+childIdentity.RunID, semconv.LinkDelegates,
			childDescriptor.TraceID, childDescriptor.RootSpanID, base)},
		{Trace: childDescriptor, Record: benchmarkLink(childDescriptor.TraceID, childDescriptor.RootSpanID,
			"run/"+childIdentity.RunID+"/delegated-from/"+identity.RunID, semconv.LinkDelegatedFrom,
			parentDescriptor.TraceID, parentDescriptor.RootSpanID, base.Add(time.Microsecond))},
		{Trace: parentDescriptor, Record: benchmarkEvent(parentDescriptor.TraceID, parentDescriptor.RootSpanID,
			"run/"+identity.RunID+"/delegation/created", "nano.delegation.created", base.Add(2*time.Microsecond), attributes)},
		{Trace: childDescriptor, Record: benchmarkEvent(childDescriptor.TraceID, childDescriptor.RootSpanID,
			"run/"+childIdentity.RunID+"/delegation/completed", "nano.delegation.terminal", base.Add(3*time.Microsecond), attributes)},
		{Trace: parentDescriptor, Record: benchmarkEvent(parentDescriptor.TraceID, parentDescriptor.RootSpanID,
			"run/"+identity.RunID+"/delegation/wake/completed", "nano.delegation.parent_wake", base.Add(4*time.Microsecond), attributes)},
		{Trace: parentDescriptor, Record: benchmarkEvent(parentDescriptor.TraceID, parentDescriptor.RootSpanID,
			"run/"+identity.RunID+"/delegation/consumed/completed", "nano.delegation.consumed", base.Add(5*time.Microsecond), attributes)},
	}
	envelopes := make([]agentbatch.Envelope, 0, len(parent.Envelopes)+len(child.Envelopes)+len(relationships))
	envelopes = append(envelopes, parent.Envelopes...)
	envelopes = append(envelopes, child.Envelopes...)
	envelopes = append(envelopes, relationships...)
	return RootFixture{Identity: identity, TotalAgentRuns: 2, Envelopes: envelopes}
}

func buildRetryRecoveryFixture(identity RootIdentity, eventEpoch time.Time) RootFixture {
	traceID := agentobs.TraceID(identity.TraceID)
	suffix := identity.TraceID[len("trace-"):]
	rootID := agentobs.SpanID("root-" + suffix)
	attemptOneID := agentobs.SpanID("attempt-1-" + suffix)
	attemptTwoID := agentobs.SpanID("attempt-2-" + suffix)
	modelOneID := agentobs.SpanID("model-1-" + suffix)
	modelTwoID := agentobs.SpanID("model-2-" + suffix)
	publicationID := agentobs.SpanID("publication-" + suffix)
	descriptor := collector.TraceDescriptor{
		TraceID: traceID, WorkloadKind: collector.WorkloadAgentRun, WorkloadID: identity.RunID,
		RunID: identity.RunID, ChatID: identity.ChatID, NotebookID: identity.NotebookID,
		RootSpanID: rootID, AgentName: "nano-default-agent", SchemaVersion: 1, SemanticConventionVersion: 1,
	}
	base := eventEpoch.Add(time.Duration(identity.Ordinal) * time.Millisecond)
	records := make([]agentobs.Record, 0, 18)
	appendRecord := func(record agentobs.Record) {
		record.OccurredAt = base.Add(time.Duration(len(records)) * time.Microsecond)
		records = append(records, record)
	}
	newRecord := func(kind agentobs.RecordKind, spanID agentobs.SpanID, identityKey, name string, attributes ...agentobs.Attribute) agentobs.Record {
		return agentobs.Record{SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
			IdentityKey: identityKey, Kind: kind, TraceID: traceID, SpanID: spanID, Name: name, Attributes: attributes}
	}
	appendRecord(newRecord(agentobs.RecordSpanStarted, rootID, "run/"+identity.RunID+"/root/start", semconv.AgentExecution))
	appendRecord(newRecord(agentobs.RecordEvent, rootID, "run/"+identity.RunID+"/admitted", "nano.run.admitted"))
	firstAttempt := newRecord(agentobs.RecordSpanStarted, attemptOneID, "run/"+identity.RunID+"/attempt/1/start", "nano.job.attempt",
		agentobs.Int64("nano.attempt.number", 1))
	firstAttempt.ParentSpanID = rootID
	appendRecord(firstAttempt)
	firstModelStart := newRecord(agentobs.RecordSpanStarted, modelOneID, "run/"+identity.RunID+"/attempt/1/model/1/start", semconv.ModelCall,
		agentobs.String(semconv.ModelNameKey, "benchmark-model"))
	firstModelStart.ParentSpanID = attemptOneID
	appendRecord(firstModelStart)
	firstModelEnd := newRecord(agentobs.RecordSpanEnded, modelOneID, "run/"+identity.RunID+"/attempt/1/model/1/end", semconv.ModelCall,
		agentobs.String(semconv.ModelNameKey, "benchmark-model"), agentobs.String(semconv.ErrorKindKey, "transient_provider"))
	firstModelEnd.Status = agentobs.StatusError
	appendRecord(firstModelEnd)
	appendRecord(newRecord(agentobs.RecordEvent, attemptOneID, "run/"+identity.RunID+"/attempt/1/disposition", "nano.attempt.disposition",
		agentobs.String("nano.attempt.disposition", "retryable"), agentobs.String("nano.error.code", "provider_unavailable")))
	firstAttemptEnd := newRecord(agentobs.RecordSpanEnded, attemptOneID, "run/"+identity.RunID+"/attempt/1/end", "nano.job.attempt",
		agentobs.String("nano.attempt.disposition", "retryable"))
	firstAttemptEnd.Status = agentobs.StatusError
	appendRecord(firstAttemptEnd)
	secondAttempt := newRecord(agentobs.RecordSpanStarted, attemptTwoID, "run/"+identity.RunID+"/attempt/2/start", "nano.job.attempt",
		agentobs.Int64("nano.attempt.number", 2))
	secondAttempt.ParentSpanID = rootID
	appendRecord(secondAttempt)
	continues := benchmarkLink(traceID, attemptTwoID, "run/"+identity.RunID+"/attempt/2/continues/1", semconv.LinkContinues,
		traceID, attemptOneID, time.Time{})
	appendRecord(continues)
	secondModelStart := newRecord(agentobs.RecordSpanStarted, modelTwoID, "run/"+identity.RunID+"/attempt/2/model/1/start", semconv.ModelCall,
		agentobs.String(semconv.ModelNameKey, "benchmark-model"))
	secondModelStart.ParentSpanID = attemptTwoID
	appendRecord(secondModelStart)
	secondModelEnd := newRecord(agentobs.RecordSpanEnded, modelTwoID, "run/"+identity.RunID+"/attempt/2/model/1/end", semconv.ModelCall,
		agentobs.String(semconv.ModelNameKey, "benchmark-model"), agentobs.Int64(semconv.TokenInputKey, 320),
		agentobs.Int64(semconv.TokenOutputKey, 128), agentobs.Int64(semconv.TokenTotalKey, 448),
		agentobs.Bool(semconv.CostKnownKey, true), agentobs.Float64(semconv.CostAmountKey, 0.0012),
		agentobs.String(semconv.CostCurrencyKey, "USD"), agentobs.String(semconv.CostSourceKey, "benchmark"))
	secondModelEnd.Status = agentobs.StatusOK
	appendRecord(secondModelEnd)
	appendRecord(newRecord(agentobs.RecordEvent, attemptTwoID, "run/"+identity.RunID+"/checkpoint/final/1", "nano.checkpoint.accepted",
		agentobs.String("nano.checkpoint.kind", "final"), agentobs.Int64("nano.checkpoint.ordinal", 1)))
	publicationStart := newRecord(agentobs.RecordSpanStarted, publicationID, "run/"+identity.RunID+"/publication/start", "nano.publication")
	publicationStart.ParentSpanID = rootID
	appendRecord(publicationStart)
	appendRecord(newRecord(agentobs.RecordEvent, publicationID, "run/"+identity.RunID+"/publication/passed", "nano.publication.passed"))
	publicationEnd := newRecord(agentobs.RecordSpanEnded, publicationID, "run/"+identity.RunID+"/publication/end", "nano.publication")
	publicationEnd.Status = agentobs.StatusOK
	appendRecord(publicationEnd)
	secondAttemptEnd := newRecord(agentobs.RecordSpanEnded, attemptTwoID, "run/"+identity.RunID+"/attempt/2/end", "nano.job.attempt")
	secondAttemptEnd.Status = agentobs.StatusOK
	appendRecord(secondAttemptEnd)
	appendRecord(newRecord(agentobs.RecordEvent, rootID, "run/"+identity.RunID+"/terminal", "nano.run.terminal",
		agentobs.String("nano.run.status", "completed")))
	rootEnd := newRecord(agentobs.RecordSpanEnded, rootID, "run/"+identity.RunID+"/root/end", semconv.AgentExecution)
	rootEnd.Status = agentobs.StatusOK
	appendRecord(rootEnd)
	envelopes := make([]agentbatch.Envelope, len(records))
	for index, record := range records {
		envelopes[index] = agentbatch.Envelope{Trace: descriptor, Record: record}
	}
	return RootFixture{Identity: identity, TotalAgentRuns: 1, Envelopes: envelopes}
}

func benchmarkLink(traceID agentobs.TraceID, spanID agentobs.SpanID, identityKey, name string, targetTraceID agentobs.TraceID, targetSpanID agentobs.SpanID, occurredAt time.Time) agentobs.Record {
	return agentobs.Record{
		SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
		IdentityKey: identityKey, Kind: agentobs.RecordLink, TraceID: traceID, SpanID: spanID,
		TargetTraceID: targetTraceID, TargetSpanID: targetSpanID, Name: name, OccurredAt: occurredAt,
	}
}

func benchmarkEvent(traceID agentobs.TraceID, spanID agentobs.SpanID, identityKey, name string, occurredAt time.Time, attributes []agentobs.Attribute) agentobs.Record {
	return agentobs.Record{
		SchemaVersion: 1, SemanticConventionVersion: 1, PayloadVersion: 1,
		IdentityKey: identityKey, Kind: agentobs.RecordEvent, TraceID: traceID, SpanID: spanID,
		Name: name, OccurredAt: occurredAt, Attributes: append([]agentobs.Attribute(nil), attributes...),
	}
}
