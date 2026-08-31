package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
)

type mcpToolAction struct {
	definition models.ActionDefinition
	calls      []ActionRequest
	err        error
	result     *ActionResult
	attributes []agentobs.Attribute
}

func (a *mcpToolAction) Definition() models.ActionDefinition { return a.definition }
func (a *mcpToolAction) ValidateInput(raw json.RawMessage) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return errors.New("invalid input")
	}
	return nil
}
func (a *mcpToolAction) Execute(_ context.Context, request ActionRequest) (ActionResult, error) {
	a.calls = append(a.calls, request)
	if a.err != nil {
		return ActionResult{}, a.err
	}
	if a.result != nil {
		return *a.result, nil
	}
	return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"value":"5"}`), traceAttributes: a.attributes}, nil
}

type mcpToolAuthority struct {
	err   error
	calls []Attempt
}

func (a *mcpToolAuthority) CheckAuthority(_ context.Context, attempt Attempt) error {
	a.calls = append(a.calls, attempt)
	return a.err
}

func TestMCPToolPlaneUsesOfficialInMemoryProtocolAndDefinitionScope(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	calculate := testMCPAction("calculate")
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: calculate, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("web_search"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := &mcpToolAuthority{}
	host, err := NewMCPToolHost(catalog, registry, authority)
	if err != nil {
		t.Fatal(err)
	}
	attempt := Attempt{RunID: "run-mcp", JobID: "job-mcp", AttemptNo: 2, LeaseToken: "lease-mcp"}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"), Attempt: attempt,
		DefaultTimeZone: "Asia/Shanghai", RemainingActions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.ProtocolVersion() != "2026-07-28" {
		t.Fatalf("protocol=%q", session.ProtocolVersion())
	}
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(tools); strings.Join(got, ",") != "calculate,current_time,delegate.research.source-discovery.v1,search_evidence" {
		t.Fatalf("scoped tools=%v", got)
	}
	for _, tool := range tools {
		if len(tool.SHA256) != 64 || len(tool.InputSchema) == 0 ||
			(tool.Scheduling != agentcatalog.ToolOrderedSync && tool.Scheduling != agentcatalog.ToolExclusiveDelegation) {
			t.Fatalf("materialized tool=%+v", tool)
		}
	}
	result, err := session.CallTool(context.Background(), "calculate", json.RawMessage(`{"operation":"add"}`), "action-stable-1")
	if err != nil || result.Status != ActionSucceeded || string(result.Output) != `{"value":"5"}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(calculate.calls) != 1 || calculate.calls[0].ActionID != "action-stable-1" || calculate.calls[0].Attempt != attempt || len(authority.calls) != 2 {
		t.Fatalf("action calls=%+v authority=%+v", calculate.calls, authority.calls)
	}
	if _, err := session.CallTool(context.Background(), "web_search", json.RawMessage(`{}`), "action-stable-2"); !isToolErrorKind(err, ToolErrorAuthorization) {
		t.Fatalf("unallowlisted call err=%v", err)
	}
}

func TestMCPToolRegistryAcceptsParallelScheduling(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	searchEvidence := testMCPAction("search_evidence")
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: testMCPAction("calculate"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: searchEvidence, Scheduling: agentcatalog.ToolParallel},
		MCPToolRegistration{Action: testMCPAction("web_search"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := registry.Resolve("search_evidence")
	if !ok || tool.Scheduling != agentcatalog.ToolParallel {
		t.Fatalf("search_evidence tool=%+v found=%t", tool, ok)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run-parallel", JobID: "job-parallel", AttemptNo: 1, LeaseToken: "lease-parallel"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), "search_evidence", json.RawMessage(`{}`), "action-parallel-1")
	if err != nil || result.Status != ActionSucceeded {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(searchEvidence.calls) != 1 {
		t.Fatalf("calls=%+v", searchEvidence.calls)
	}
}

func TestMCPToolPlaneRoundTripsActionTraceAttributes(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	searchEvidence := testMCPAction("search_evidence")
	searchEvidence.attributes = []agentobs.Attribute{
		agentobs.Bool(TraceKeyDenseCompleted, true),
		agentobs.Int64(TraceKeyRelevanceFilteredCount, 2),
		agentobs.String(TraceKeyRelevanceFilteredIDs, `["chunk_a","chunk_b"]`),
	}
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: testMCPAction("calculate"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: searchEvidence, Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), "search_evidence", json.RawMessage(`{}`), "decision:1/action:0")
	if err != nil {
		t.Fatal(err)
	}
	// Trace attributes an Action sets on its ActionResult (e.g. RAG retrieval
	// diagnostics) must survive the in-process MCP JSON-RPC round trip
	// (mcp_tool_plane.go's executeMCPTool -> CallTool), not just a direct
	// registry.Resolve invocation, or every search_evidence call routed
	// through the MCP Tool Plane silently loses its trace attributes.
	if !reflect.DeepEqual(result.traceAttributes, searchEvidence.attributes) {
		t.Fatalf("traceAttributes=%+v want=%+v", result.traceAttributes, searchEvidence.attributes)
	}
}

func TestMCPToolPlaneMaterializesConfiguredChildAsExclusiveDelegation(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	delegate := &mcpToolAction{definition: models.ActionDefinition{
		Name:        "delegate.research.source-discovery.v1",
		Description: "Schedule the configured source-discovery child Agent.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["request"],"properties":{"request":{"type":"string","minLength":1,"maxLength":32768}}}`),
	}}
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: testMCPAction("calculate"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: delegate, Scheduling: agentcatalog.ToolScheduling("exclusive_delegation")},
	)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(toolNames(tools), ","); got != "calculate,current_time,delegate.research.source-discovery.v1,search_evidence" {
		t.Fatalf("scoped tools=%s", got)
	}
	materialized, ok := session.byName["delegate.research.source-discovery.v1"]
	if !ok || materialized.Scheduling != agentcatalog.ToolScheduling("exclusive_delegation") {
		t.Fatalf("delegation tool=%+v found=%t", materialized, ok)
	}
	if err := session.ValidateProposal([]models.ActionProposal{
		{Name: "delegate.research.source-discovery.v1", Input: json.RawMessage(`{"request":"find sources"}`)},
		{Name: "calculate", Input: json.RawMessage(`{}`)},
	}); err == nil || !strings.Contains(err.Error(), "exclusive") {
		t.Fatalf("mixed delegation proposal err=%v", err)
	}
}

func TestMCPToolPlaneFailsClosedForMissingActionIDAndLostLease(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	action := testMCPAction("calculate")
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: action, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := &mcpToolAuthority{}
	host, err := NewMCPToolHost(catalog, registry, authority)
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if _, err := session.CallTool(context.Background(), "calculate", json.RawMessage(`{}`), ""); !isToolErrorKind(err, ToolErrorInvariant) {
		t.Fatalf("missing action ID err=%v", err)
	}
	authority.err = ErrLeaseLost
	if _, err := session.CallTool(context.Background(), "calculate", json.RawMessage(`{}`), "action-one"); !isToolErrorKind(err, ToolErrorAuthorization) {
		t.Fatalf("lost Lease err=%v", err)
	}
	if len(action.calls) != 0 {
		t.Fatalf("action executed without authority: %+v", action.calls)
	}
}

func TestMCPToolPlanePreservesAdapterErrorClassification(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	action := testMCPAction("calculate")
	action.err = &ToolCallError{Kind: ToolErrorInvariant, Code: "delegation_relationship_invalid"}
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: action, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	_, err = session.CallTool(context.Background(), "calculate", json.RawMessage(`{}`), "decision:1/action:0")
	var toolErr *ToolCallError
	if !errors.As(err, &toolErr) || toolErr.Kind != ToolErrorInvariant || toolErr.Code != "delegation_relationship_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestControllerExecutesAcceptedProductionActionOnlyAcrossMCP(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	direct := testMCPAction("calculate")
	directRegistry, err := NewActionRegistry(direct, testMCPAction("current_time"), testMCPAction("search_evidence"))
	if err != nil {
		t.Fatal(err)
	}
	mcpAction := testMCPAction("calculate")
	mcpRegistry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: mcpAction, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &controllerRuntimeStub{execution: defaultControllerExecution()}
	host, err := NewMCPToolHost(catalog, mcpRegistry, runtime)
	if err != nil {
		t.Fatal(err)
	}
	model := &decisionModelStub{decisions: []models.ModelDecision{
		{Proposal: &models.ActionProposalBatch{Actions: []models.ActionProposal{{Name: "calculate", Input: json.RawMessage(`{"operation":"add"}`)}}}},
		{Final: &models.FinalDraft{Text: "done"}},
	}}
	controller := NewMCPController(runtime, model, directRegistry, host, agentcatalog.MustParseReference("chat.leader@1"))
	if err := controller.Execute(context.Background(), runtime.execution.Attempt); err != nil {
		t.Fatal(err)
	}
	if len(direct.calls) != 0 || len(mcpAction.calls) != 1 || mcpAction.calls[0].ActionID == "" {
		t.Fatalf("direct=%d MCP=%+v", len(direct.calls), mcpAction.calls)
	}
}

func TestMCPToolPlaneReturnsRecoverableWebSearchFailureToTheModel(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	provider := &webSearchActionProvider{err: websearch.ErrRateLimited}
	registry, err := NewMCPToolRegistry(MCPToolRegistration{Action: NewWebSearchAction(provider), Scheduling: agentcatalog.ToolOrderedSync})
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("research.source-discovery@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), "web_search", json.RawMessage(`{"queries":["alpha"]}`), "research:web_search:0")
	if err != nil || result.Status != ActionDomainError || result.ErrorCode != "web_search_rate_limited" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestMCPToolPlanePreservesStructuredDomainError(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	calculate := testMCPAction("calculate")
	calculate.result = &ActionResult{Status: ActionDomainError, Error: &ActionError{
		Kind: "domain", Code: "todo_revision_conflict", Message: "The TODO list revision is stale.",
		Suggestion: "Use the current revision.", Retryable: true,
	}}
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: calculate, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), "calculate", json.RawMessage(`{}`), "decision:1/action:0")
	if err != nil || result.Error == nil || result.Error.Code != "todo_revision_conflict" || !result.Error.Retryable {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestMCPToolPlaneRecordsFilteredToolReason(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: testMCPAction("calculate"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: NewSearchEvidenceAction(nil), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	host, err := NewMCPToolHost(catalog, registry, &mcpToolAuthority{})
	if err != nil {
		t.Fatal(err)
	}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"),
		Attempt:    Attempt{RunID: "run", JobID: "job", AttemptNo: 1, LeaseToken: "lease"}, RemainingActions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tracer, exporter, ctx := instrumentationTestTracer(t)
	definitions, err := session.ActionDefinitions(ctx, ActionPolicy{RemainingActions: 1, Execution: &Execution{}}, tracer)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Name == "search_evidence" {
			t.Fatalf("filtered search_evidence leaked: %+v", definitions)
		}
	}
	for _, record := range exporter.Records() {
		if record.Kind == agentobs.RecordEvent && record.Name == TraceEventToolFiltered {
			if reason := traceRecordAttribute(record, TraceKeyToolReasonCode); reason != "no_sources_selected" {
				t.Fatalf("filtered reason=%q", reason)
			}
			return
		}
	}
	t.Fatal("MCP filtered tool trace event was not recorded")
}

func testMCPAction(name string) *mcpToolAction {
	return &mcpToolAction{definition: models.ActionDefinition{
		Name: name, Description: "Execute the bounded " + name + " capability.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}}
}

func testDelegationMCPRegistration() MCPToolRegistration {
	return MCPToolRegistration{Action: &mcpToolAction{definition: models.ActionDefinition{
		Name:        "delegate.research.source-discovery.v1",
		Description: "Schedule the configured source-discovery child Agent.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["request"],"properties":{"request":{"type":"string"}}}`),
	}}, Scheduling: agentcatalog.ToolExclusiveDelegation}
}

func toolNames(tools []MaterializedMCPTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func isToolErrorKind(err error, kind ToolErrorKind) bool {
	var toolErr *ToolCallError
	return errors.As(err, &toolErr) && toolErr.Kind == kind
}
