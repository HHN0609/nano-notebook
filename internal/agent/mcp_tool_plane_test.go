package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

type mcpToolAction struct {
	definition models.ActionDefinition
	calls      []ActionRequest
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
	return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"value":"5"}`)}, nil
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
	if got := toolNames(tools); strings.Join(got, ",") != "calculate,current_time,search_evidence" {
		t.Fatalf("scoped tools=%v", got)
	}
	for _, tool := range tools {
		if len(tool.SHA256) != 64 || tool.Scheduling != agentcatalog.ToolOrderedSync || len(tool.InputSchema) == 0 {
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

func TestMCPToolPlaneFailsClosedForMissingActionIDAndLostLease(t *testing.T) {
	catalog, _ := agentcatalog.LoadEmbedded()
	action := testMCPAction("calculate")
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: action, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("current_time"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
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

func testMCPAction(name string) *mcpToolAction {
	return &mcpToolAction{definition: models.ActionDefinition{
		Name: name, Description: "Execute the bounded " + name + " capability.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
	}}
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
