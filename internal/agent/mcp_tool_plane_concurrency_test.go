package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

// delayedMCPAction is a thread-safe test double whose Execute sleeps before
// returning. Unlike mcpToolAction, it may safely be invoked from multiple
// goroutines for the same instance: call recording is mutex-guarded rather
// than a bare slice append.
type delayedMCPAction struct {
	definition models.ActionDefinition
	delay      time.Duration
	output     json.RawMessage

	mu    sync.Mutex
	calls []ActionRequest
}

func newDelayedMCPAction(name string, delay time.Duration, output json.RawMessage) *delayedMCPAction {
	return &delayedMCPAction{
		definition: models.ActionDefinition{
			Name: name, Description: "Execute the bounded " + name + " capability.",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
		},
		delay:  delay,
		output: output,
	}
}

func (a *delayedMCPAction) Definition() models.ActionDefinition { return a.definition }
func (a *delayedMCPAction) ValidateInput(raw json.RawMessage) error {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return errors.New("invalid input")
	}
	return nil
}
func (a *delayedMCPAction) Execute(_ context.Context, request ActionRequest) (ActionResult, error) {
	a.mu.Lock()
	a.calls = append(a.calls, request)
	a.mu.Unlock()
	time.Sleep(a.delay)
	return ActionResult{Status: ActionSucceeded, Output: a.output}, nil
}

// TestMCPAttemptSessionCallToolRunsConcurrently proves that two CallTool
// invocations issued from separate goroutines over the *same*
// MCPAttemptSession (same clientSession/serverSession pair) execute
// concurrently — i.e. the server dispatches and runs both Execute calls in
// parallel rather than serializing them — rather than being serialized by
// the MCP host, the go-sdk's jsonrpc2 transport, or nano's own
// attemptToolContext locking. This is the load-bearing assumption for a
// parallel ToolBatchExecutor: if this test fails or races under -race,
// ToolBatchExecutor cannot call CallTool directly from multiple goroutines
// and would need per-action sessions or serialization.
func TestMCPAttemptSessionCallToolRunsConcurrently(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	const delay = 80 * time.Millisecond
	calculate := newDelayedMCPAction("calculate", delay, json.RawMessage(`{"value":"5"}`))
	currentTime := newDelayedMCPAction("current_time", delay, json.RawMessage(`{"value":"now"}`))
	registry, err := NewMCPToolRegistry(
		MCPToolRegistration{Action: calculate, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: currentTime, Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("search_evidence"), Scheduling: agentcatalog.ToolOrderedSync},
		MCPToolRegistration{Action: testMCPAction("web_search"), Scheduling: agentcatalog.ToolOrderedSync},
		testDelegationMCPRegistration(),
	)
	if err != nil {
		t.Fatal(err)
	}
	authority := &concurrentMCPAuthority{}
	host, err := NewMCPToolHost(catalog, registry, authority)
	if err != nil {
		t.Fatal(err)
	}
	attempt := Attempt{RunID: "run-concurrent", JobID: "job-concurrent", AttemptNo: 1, LeaseToken: "lease-concurrent"}
	session, err := host.OpenAttempt(context.Background(), AttemptToolScope{
		Definition: agentcatalog.MustParseReference("chat.leader@1"), Attempt: attempt,
		DefaultTimeZone: "Asia/Shanghai", RemainingActions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	started := time.Now()
	var wg sync.WaitGroup
	results := make([]ActionResult, 2)
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0], errs[0] = session.CallTool(context.Background(), "calculate", json.RawMessage(`{"operation":"add"}`), "action-concurrent-1")
	}()
	go func() {
		defer wg.Done()
		results[1], errs[1] = session.CallTool(context.Background(), "current_time", json.RawMessage(`{}`), "action-concurrent-2")
	}()
	wg.Wait()
	elapsed := time.Since(started)

	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errs=%v", errs)
	}
	if results[0].Status != ActionSucceeded || results[1].Status != ActionSucceeded {
		t.Fatalf("results=%+v", results)
	}
	if len(calculate.recordedCalls()) != 1 || len(currentTime.recordedCalls()) != 1 {
		t.Fatalf("call counts: calculate=%d current_time=%d", len(calculate.recordedCalls()), len(currentTime.recordedCalls()))
	}
	// Serial dispatch of two delay-sleeping Execute calls would take at
	// least 2*delay; concurrent dispatch takes roughly delay.
	if elapsed >= 2*delay {
		t.Fatalf("CallTool invocations ran serially: elapsed=%s delay=%s", elapsed, delay)
	}
}

func (a *delayedMCPAction) recordedCalls() []ActionRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]ActionRequest(nil), a.calls...)
}

// concurrentMCPAuthority is a thread-safe MCPToolAuthority test double.
// mcpToolAuthority (mcp_tool_plane_test.go) is intentionally single-threaded
// — it backs sequential-only tests — and races when invoked from multiple
// goroutines, which is exactly what executeMCPTool does for parallel tool
// calls (mcp_tool_plane.go:612). Production wires a *PostgresRuntime here
// instead, which is concurrency-safe (each call opens its own transaction).
type concurrentMCPAuthority struct {
	mu    sync.Mutex
	calls []Attempt
}

func (a *concurrentMCPAuthority) CheckAuthority(_ context.Context, attempt Attempt) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, attempt)
	return nil
}
