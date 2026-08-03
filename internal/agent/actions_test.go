package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

func TestActionRegistryDiscoversDeterministicallyAndFiltersRunPolicy(t *testing.T) {
	currentTime := stubAction{name: "current_time"}
	calculate := stubAction{name: "calculate"}
	registry, err := NewActionRegistry(currentTime, calculate)
	if err != nil {
		t.Fatal(err)
	}

	definitions := registry.Definitions(context.Background(), ActionPolicy{RemainingActions: 2})
	if len(definitions) != 2 || definitions[0].Name != "calculate" || definitions[1].Name != "current_time" {
		t.Fatalf("deterministic definitions = %+v", definitions)
	}
	filtered := registry.Definitions(context.Background(), ActionPolicy{RemainingActions: 1, AllowedNames: map[string]bool{"current_time": true}})
	if len(filtered) != 1 || filtered[0].Name != "current_time" {
		t.Fatalf("filtered definitions = %+v", filtered)
	}
	if definitions := registry.Definitions(context.Background(), ActionPolicy{}); len(definitions) != 0 {
		t.Fatalf("zero-budget definitions = %+v", definitions)
	}
	resolved, ok := registry.Resolve("calculate")
	if !ok || resolved.Definition().Name != "calculate" {
		t.Fatalf("resolved calculate = %T ok=%t", resolved, ok)
	}
	if _, ok := registry.Resolve("missing"); ok {
		t.Fatal("resolved an unregistered Action")
	}
}

func TestActionRegistryRejectsDuplicateNames(t *testing.T) {
	if _, err := NewActionRegistry(stubAction{name: "calculate"}, stubAction{name: "calculate"}); err == nil {
		t.Fatal("duplicate Action registration error = nil")
	}
}

func TestActionRegistrySnapshotsDefinitionsAtStartup(t *testing.T) {
	action := &mutableStubAction{definition: models.ActionDefinition{
		Name: "current_time", Description: "Read time.", InputSchema: json.RawMessage(`{"type":"object"}`),
	}}
	registry, err := NewActionRegistry(action)
	if err != nil {
		t.Fatal(err)
	}
	action.definition.Name = "mutated"
	action.definition.Description = "Mutated."
	action.definition.InputSchema[2] = 'X'

	definitions := registry.Definitions(context.Background(), ActionPolicy{RemainingActions: 1})
	if len(definitions) != 1 || definitions[0].Name != "current_time" || definitions[0].Description != "Read time." || string(definitions[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("definitions changed after startup = %+v", definitions)
	}
	if _, ok := registry.Resolve("current_time"); !ok {
		t.Fatal("startup name no longer resolves")
	}
}

func TestActionRegistryRejectsInvalidDefinitions(t *testing.T) {
	tests := []models.ActionDefinition{
		{Name: "Not Canonical", Description: "Invalid name.", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "valid_name", Description: "", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "valid_name", Description: "Invalid schema.", InputSchema: json.RawMessage(`[]`)},
	}
	for _, definition := range tests {
		action := &mutableStubAction{definition: definition}
		if _, err := NewActionRegistry(action); err == nil {
			t.Fatalf("invalid definition accepted: %+v", definition)
		}
	}
}

func TestActionRegistryValidatesWholeProposalStructureBeforeAcceptance(t *testing.T) {
	registry, err := NewActionRegistry(NewCalculateAction(), NewCurrentTimeAction(nil))
	if err != nil {
		t.Fatal(err)
	}
	valid := []models.ActionProposal{
		{Name: "calculate", Input: json.RawMessage(`{"operation":"divide","operands":["1","0"]}`)},
		{Name: "current_time", Input: json.RawMessage(`{"time_zone":"Mars/Olympus"}`)},
	}
	if err := registry.ValidateProposal(valid); err != nil {
		t.Fatalf("domain-error-capable inputs rejected structurally: %v", err)
	}
	tests := []struct {
		name    string
		actions []models.ActionProposal
	}{
		{name: "unknown Action", actions: []models.ActionProposal{{Name: "network", Input: json.RawMessage(`{}`)}}},
		{name: "calculate wrong type", actions: []models.ActionProposal{{Name: "calculate", Input: json.RawMessage(`{"operation":1,"operands":["1","2"]}`)}}},
		{name: "current time unknown field", actions: []models.ActionProposal{{Name: "current_time", Input: json.RawMessage(`{"locale":"en"}`)}}},
		{name: "trailing JSON", actions: []models.ActionProposal{{Name: "calculate", Input: json.RawMessage(`{} {}`)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := registry.ValidateProposal(tt.actions); err == nil {
				t.Fatal("structurally invalid proposal accepted")
			}
		})
	}
}

func TestActionResultRequiresOneValidVariant(t *testing.T) {
	tests := []struct {
		name    string
		result  ActionResult
		wantErr bool
	}{
		{name: "success", result: ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"value":"3"}`)}},
		{name: "domain error", result: ActionResult{Status: ActionDomainError, ErrorCode: "division_by_zero"}},
		{name: "success without output", result: ActionResult{Status: ActionSucceeded}, wantErr: true},
		{name: "success with error", result: ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{}`), ErrorCode: "unexpected"}, wantErr: true},
		{name: "domain error with output", result: ActionResult{Status: ActionDomainError, Output: json.RawMessage(`{}`), ErrorCode: "invalid"}, wantErr: true},
		{name: "domain error without code", result: ActionResult{Status: ActionDomainError}, wantErr: true},
		{name: "unknown status", result: ActionResult{Status: ActionResultStatus("unknown")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestActionRegistryRecordsFilteredToolReason(t *testing.T) {
	registry, err := NewActionRegistry(NewSearchEvidenceAction(&evidenceSearchStub{}))
	if err != nil {
		t.Fatal(err)
	}
	tracer, exporter, ctx := instrumentationTestTracer(t)
	got := registry.Definitions(ctx, ActionPolicy{RemainingActions: 1, Execution: &Execution{}}, tracer)
	if len(got) != 0 {
		t.Fatalf("filtered definitions=%+v", got)
	}
	for _, record := range exporter.Records() {
		if record.Kind == agentobs.RecordEvent && record.Name == TraceEventToolFiltered {
			reason := traceRecordAttribute(record, TraceKeyToolReasonCode)
			if reason != "no_sources_selected" {
				t.Fatalf("filtered reason=%q", reason)
			}
			return
		}
	}
	t.Fatal("filtered tool trace event was not recorded")
}

func TestActionRegistryDoesNotRecordFilteredEventWhenAvailable(t *testing.T) {
	registry, err := NewActionRegistry(NewSearchEvidenceAction(&evidenceSearchStub{}))
	if err != nil {
		t.Fatal(err)
	}
	tracer, exporter, ctx := instrumentationTestTracer(t)
	before := countTraceEvents(exporter.Records(), TraceEventToolFiltered)
	got := registry.Definitions(ctx, ActionPolicy{RemainingActions: 1, Execution: &Execution{SelectedSourceCount: 1}}, tracer)
	if len(got) != 1 || countTraceEvents(exporter.Records(), TraceEventToolFiltered) != before {
		t.Fatalf("definitions=%+v filtered events before=%d after=%d", got, before, countTraceEvents(exporter.Records(), TraceEventToolFiltered))
	}
}

type stubAction struct {
	name string
}

func (a stubAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{Name: a.name, Description: "Test " + a.name, InputSchema: json.RawMessage(`{"type":"object"}`)}
}

func (stubAction) ValidateInput(json.RawMessage) error { return nil }

func (stubAction) Execute(context.Context, ActionRequest) (ActionResult, error) {
	return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"ok":true}`)}, nil
}

type mutableStubAction struct {
	definition models.ActionDefinition
}

func (a *mutableStubAction) Definition() models.ActionDefinition {
	return a.definition
}

func (*mutableStubAction) ValidateInput(json.RawMessage) error { return nil }

func (*mutableStubAction) Execute(context.Context, ActionRequest) (ActionResult, error) {
	return ActionResult{Status: ActionSucceeded, Output: json.RawMessage(`{"ok":true}`)}, nil
}

func traceRecordAttribute(record agentobs.Record, key string) string {
	for _, attribute := range record.Attributes {
		if attribute.Key == key && attribute.Value.Kind == agentobs.ValueString {
			return attribute.Value.String
		}
	}
	return ""
}

func countTraceEvents(records []agentobs.Record, name string) int {
	count := 0
	for _, record := range records {
		if record.Kind == agentobs.RecordEvent && record.Name == name {
			count++
		}
	}
	return count
}
