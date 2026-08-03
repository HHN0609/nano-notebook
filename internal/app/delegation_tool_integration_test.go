package app_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/app"
	"github.com/huangxinxinyu/nano-notebook/internal/collector"
	"github.com/huangxinxinyu/nano-notebook/internal/jobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
	"github.com/jackc/pgx/v5"
)

type delegationDecisionModel struct {
	calls int
}

func (m *delegationDecisionModel) Decide(_ context.Context, request models.ModelRequest) (models.ModelOutcome, error) {
	m.calls++
	if m.calls == 1 {
		return models.ModelOutcome{ModelDecision: models.ModelDecision{
			Proposal: &models.ActionProposalBatch{Actions: []models.ActionProposal{{
				Name:  "delegate.research.source-discovery.v1",
				Input: json.RawMessage(`{"request":"find current sources"}`),
			}}},
		}, Metadata: models.ModelCallMetadata{
			RequestedModel: request.Model, SelectedProvider: "test", SelectedModel: "test",
			ResultKind: models.ModelResultActionProposal, FinishReason: "tool_calls",
		}}, nil
	}
	return models.ModelOutcome{ModelDecision: models.ModelDecision{Final: &models.FinalDraft{Text: "Sources are ready."}},
		Metadata: models.ModelCallMetadata{
			RequestedModel: request.Model, SelectedProvider: "test", SelectedModel: "test",
			ResultKind: models.ModelResultFinalDraft, FinishReason: "stop",
		}}, nil
}

type availableResearchProvider struct{}

func (availableResearchProvider) ResearchAvailable() bool { return true }

func TestConfiguredLeaderModelChoosesDelegationToolAndResumesThroughController(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "model-chosen-delegation@example.com")
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	sink := &capturingDirectTraceSink{}
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"), TraceSink: sink,
	}, api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c130", "content": "find current sources", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("admission status=%d body=%s", response.Code, response.Body.String())
	}
	var admitted struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &admitted)

	queue := jobs.NewQueueWithTraceSink(api.db.Pool(), sink)
	parent, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || parent.RunID != admitted.RunID {
		t.Fatalf("parent claim=%+v ok=%t err=%v", parent, ok, err)
	}

	provider := &recordingResearchProvider{results: map[string][]websearch.Candidate{
		"configured research": {{Title: "Source", URL: "https://example.com/source", DisplayURL: "example.com", Description: "Found", Rank: 1}},
	}}
	registrations := []agent.MCPToolRegistration{
		{Action: agent.NewCalculateAction(), Scheduling: agentcatalog.ToolOrderedSync},
		{Action: agent.NewCurrentTimeAction(nil), Scheduling: agentcatalog.ToolOrderedSync},
		{Action: agent.NewSearchEvidenceAction(nil), Scheduling: agentcatalog.ToolOrderedSync},
		{Action: agent.NewWebSearchAction(provider), Scheduling: agentcatalog.ToolOrderedSync},
	}
	generated, err := agent.NewConfiguredDelegationToolRegistrations(catalog, api.db.Pool(), availableResearchProvider{}, sink)
	if err != nil {
		t.Fatal(err)
	}
	registrations = append(registrations, generated...)
	mcpRegistry, err := agent.NewMCPToolRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	postgresRuntime := agent.NewPostgresRuntime(api.db.Pool(), agent.BareSystemPrompt, nil, agent.WithTraceSink(sink))
	mcpHost, err := agent.NewMCPToolHost(catalog, mcpRegistry, postgresRuntime)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := catalog.ResolveDefinition(agentcatalog.MustParseReference("chat.leader@1"))
	child := root.Children[0]
	resultContract, _ := catalog.ResolveContract(agentcatalog.MustParseReference("research.discovery-result@1"))
	directRegistry, err := agent.NewActionRegistry(
		agent.NewCalculateAction(), agent.NewCurrentTimeAction(nil), agent.NewSearchEvidenceAction(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	controller := agent.NewMCPController(postgresRuntime, &delegationDecisionModel{}, directRegistry, mcpHost, root.Reference())
	leaderRuntime := agent.NewLeaderExecutor(
		api.db.Pool(), controller, fixedResearchPlanner{queries: []string{"configured research"}}, provider,
		agent.WithResearchMCPToolPlane(mcpHost, child),
		agent.WithResearchResultContract(resultContract),
		agent.WithLeaderTraceSink(sink),
	)

	execution, err := postgresRuntime.Load(context.Background(), attemptFromClaim(parent))
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := mcpHost.OpenAttempt(context.Background(), agent.AttemptToolScope{
		Definition: root.Reference(), Attempt: attemptFromClaim(parent), DefaultTimeZone: execution.TimeZone,
		RemainingActions: execution.ActionLimit, Deadline: execution.DeadlineAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelTools, err := inspection.ActionDefinitions(context.Background(), agent.ActionPolicy{
		RemainingActions: execution.ActionLimit, Execution: &execution,
	})
	if err == nil {
		err = inspection.ValidateProposal([]models.ActionProposal{{
			Name:  "delegate.research.source-discovery.v1",
			Input: json.RawMessage(`{"request":"find current sources"}`),
		}})
	}
	if closeErr := inspection.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	foundDelegate := false
	for _, definition := range modelTools {
		if definition.Name == "delegate.research.source-discovery.v1" {
			foundDelegate = true
			break
		}
	}
	if !foundDelegate {
		t.Fatalf("delegation tool not offered execution=%+v tools=%+v", execution, modelTools)
	}
	if err := leaderRuntime.Execute(context.Background(), attemptFromClaim(parent)); err != nil {
		t.Fatalf("parent scheduling error=%v", err)
	}
	var parentStatus, parentJobStatus, childRunID, childKind string
	var actionID *string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select parent.status,parent_job.status,child.id,child.runtime_kind,delegation.action_id
		from agent_runs parent
		join agent_jobs parent_job on parent_job.run_id=parent.id
		join agent_run_delegations delegation on delegation.parent_run_id=parent.id
		join agent_runs child on child.id=delegation.child_run_id
		where parent.id=$1
	`, admitted.RunID).Scan(&parentStatus, &parentJobStatus, &childRunID, &childKind, &actionID); err != nil {
		t.Fatal(err)
	}
	if parentStatus != "running" || parentJobStatus != "waiting" || childKind != "configured" ||
		actionID == nil || *actionID != "decision:1/action:0" {
		t.Fatalf("parent=%s/%s child=%s kind=%s action=%v", parentStatus, parentJobStatus, childRunID, childKind, actionID)
	}

	childClaim, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || childClaim.RunID != childRunID {
		t.Fatalf("child claim=%+v ok=%t err=%v", childClaim, ok, err)
	}
	if resolution := agent.NewResearchDefinitionExecutor(leaderRuntime).ExecuteAttempt(context.Background(), attemptFromClaim(childClaim)); resolution.Disposition != agent.AttemptCompleted {
		t.Fatalf("child resolution=%+v", resolution)
	}

	resumedParent, ok, err := queue.ClaimNext(context.Background())
	if err != nil || !ok || resumedParent.RunID != admitted.RunID {
		t.Fatalf("resumed parent claim=%+v ok=%t err=%v", resumedParent, ok, err)
	}
	if resolution := agent.NewChatLeaderDefinitionExecutor(leaderRuntime).ExecuteAttempt(context.Background(), attemptFromClaim(resumedParent)); resolution.Disposition != agent.AttemptCompleted {
		t.Fatalf("resumed parent resolution=%+v", resolution)
	}

	var parentTerminal, productTerminal, delegationState string
	var outputMessageID *string
	if err := api.db.Pool().QueryRow(context.Background(), `
		select parent.status,product.status,delegation.state,parent.output_message_id
		from agent_runs parent
		join chat_runs product on product.root_agent_run_id=parent.id
		join agent_run_delegations delegation on delegation.parent_run_id=parent.id
		where parent.id=$1
	`, admitted.RunID).Scan(&parentTerminal, &productTerminal, &delegationState, &outputMessageID); err != nil {
		t.Fatal(err)
	}
	if parentTerminal != "completed" || productTerminal != "completed" || delegationState != "succeeded" || outputMessageID == nil {
		t.Fatalf("terminal parent=%s product=%s delegation=%s output=%v", parentTerminal, productTerminal, delegationState, outputMessageID)
	}
	var output string
	if err := api.db.Pool().QueryRow(context.Background(), `select content from chat_messages where id=$1`, *outputMessageID).Scan(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "Sources are ready.") {
		t.Fatalf("final output=%q", output)
	}
	assertTraceEnvelopeOrder(t, sink)
	assertDelegationTraceProjects(t, sink)
}

func assertTraceEnvelopeOrder(t *testing.T, sink *capturingDirectTraceSink) {
	t.Helper()
	starts := make(map[string]bool)
	for _, envelope := range sink.envelopes {
		key := string(envelope.Trace.TraceID) + "/" + string(envelope.Record.SpanID)
		switch envelope.Record.Kind {
		case agentobs.RecordSpanStarted:
			starts[key] = true
		case agentobs.RecordSpanEnded:
			if !starts[key] {
				t.Fatalf("trace span terminal before start: trace=%s span=%s record=%+v envelopes=%d", envelope.Trace.TraceID, envelope.Record.SpanID, envelope.Record, len(sink.envelopes))
			}
		}
	}
}

func assertDelegationTraceProjects(t *testing.T, sink *capturingDirectTraceSink) {
	t.Helper()
	store := collector.NewMemoryStore()
	descriptors := make(map[agentobs.TraceID]collector.TraceDescriptor)
	ctx := context.Background()
	for _, envelope := range sink.envelopes {
		if _, seen := descriptors[envelope.Trace.TraceID]; !seen {
			descriptors[envelope.Trace.TraceID] = envelope.Trace
		}
		hash, err := envelope.Record.CanonicalHash()
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CommitTraceChunk(ctx, collector.TraceChunk{
			Trace: envelope.Trace, SequenceAuthority: collector.SequenceAuthorityCollector,
			Records: []collector.SequencedRecord{{Record: envelope.Record, CanonicalSHA256: hex.EncodeToString(hash[:])}},
		})
		if err != nil {
			t.Fatalf("commit trace envelope %+v: %v", envelope.Record, err)
		}
	}
	for traceID, descriptor := range descriptors {
		records := store.Records(traceID)
		if _, err := collector.BuildTraceProjection(collector.StoredTrace{
			Trace: descriptor, CommittedThrough: len(records), Records: records,
		}); err != nil {
			t.Fatalf("project trace %s with %d records: %v", traceID, len(records), err)
		}
	}
}

func TestConfiguredDelegationActionSchedulesDirectly(t *testing.T) {
	api, sessionCookie, csrfCookie, chatID := newChatFixture(t, "direct-delegation-schedule@example.com")
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	api.server = app.NewServer(app.Config{
		CookieSecure: false, AgentCatalog: catalog,
		AgentRelease: agentcatalog.MustParseReference("nano.default@1"),
	}, api.db)
	api.handler = api.server.Handler()
	response := api.postJSONWithCookieAndCSRF(t, "/api/v1/chats/"+chatID+"/messages", map[string]any{
		"id": "0190cdd2-5f2d-7ad8-b3f5-1b588788c131", "content": "schedule directly", "time_zone": "Asia/Shanghai",
	}, sessionCookie, csrfCookie, csrfCookie.Value, "")
	if response.Code != http.StatusAccepted {
		t.Fatalf("admission status=%d body=%s", response.Code, response.Body.String())
	}
	var admitted struct {
		RunID string `json:"run_id"`
	}
	decodeBody(t, response, &admitted)
	parent, ok, err := jobs.NewQueue(api.db.Pool()).ClaimNext(context.Background())
	if err != nil || !ok || parent.RunID != admitted.RunID {
		t.Fatalf("parent claim=%+v ok=%t err=%v", parent, ok, err)
	}
	root, _ := catalog.ResolveDefinition(agentcatalog.MustParseReference("chat.leader@1"))
	generated, err := agent.NewConfiguredDelegationToolRegistrations(catalog, api.db.Pool(), availableResearchProvider{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := generated[0].Action.Execute(context.Background(), agent.ActionRequest{
		ActionID: "decision:1/action:0", Input: json.RawMessage(`{"request":"schedule directly"}`),
		Attempt: attemptFromClaim(parent), Definition: root.Reference(), DefinitionSHA256: root.SHA256,
	})
	if err != nil {
		t.Fatalf("direct delegation execute: %v", err)
	}
	if result.Status != agent.ActionSucceeded || len(result.Output) == 0 {
		t.Fatalf("direct delegation result=%+v", result)
	}
}

type fixedResearchPlanner struct{ queries []string }

func (p fixedResearchPlanner) ExpandQueries(context.Context, agent.ResearchPlanRequest) ([]string, error) {
	return append([]string(nil), p.queries...), nil
}

type recordingNormalExecutor struct{ calls int }

func (e *recordingNormalExecutor) Execute(context.Context, agent.Attempt) error {
	e.calls++
	return nil
}

type recordingResearchProvider struct {
	requests []websearch.Request
	results  map[string][]websearch.Candidate
}

func (p *recordingResearchProvider) Search(_ context.Context, request websearch.Request) ([]websearch.Candidate, error) {
	p.requests = append(p.requests, request)
	return p.results[request.Query], nil
}

func newConfiguredServerConfig(base app.Config) app.Config {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		panic(err)
	}
	base.AgentCatalog = catalog
	base.AgentRelease = agentcatalog.MustParseReference("nano.default@1")
	return base
}

func admitLegacyRunForTest(t *testing.T, api *testAPI, chatID, messageID, runID, jobID string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := api.db.Pool().QueryRow(ctx, `select creator_user_id from chat_chats where id=$1`, chatID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	scope, err := agent.NewTraceScope(agent.DiscardTraceSink{})
	if err != nil {
		t.Fatal(err)
	}
	traceCtx := agent.ContextWithTraceScope(ctx, scope)
	if err := api.db.WithRequestPrincipal(traceCtx, userID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(traceCtx, `insert into chat_messages(id,chat_id,role,content) values($1,$2,'user','legacy fixture')`, messageID, chatID); err != nil {
			return err
		}
		if err := agent.NewStore(tx).CreateQueued(traceCtx, runID, userID, chatID, messageID, "aliyun/qwen-plus", agent.BarePromptVersion, "UTC", agent.DefaultRunConfig("nano-interactive-v1")); err != nil {
			return err
		}
		if err := jobs.NewStore(tx).CreateAgentRun(traceCtx, jobID, runID); err != nil {
			return err
		}
		return agent.StartRunTraceInTx(traceCtx, tx, runID, "aliyun/qwen-plus", agent.BarePromptVersion, nil)
	}); err != nil {
		t.Fatal(err)
	}
	return runID
}
