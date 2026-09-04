package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type discoverSourcesBackendStub struct {
	request DiscoverSourcesRequest
	result  DiscoverSourcesResult
	err     error
}

func (s *discoverSourcesBackendStub) Discover(_ context.Context, request DiscoverSourcesRequest) (DiscoverSourcesResult, error) {
	s.request = request
	return s.result, s.err
}

type unavailableDiscoveryProvider struct{}

func (unavailableDiscoveryProvider) ResearchAvailable() bool { return false }

func TestDiscoverSourcesActionAcceptsOneToThreeDistinctQueries(t *testing.T) {
	action := NewDiscoverSourcesAction(&discoverSourcesBackendStub{}, nil)
	definition := action.Definition()
	if definition.Name != "discover_sources" {
		t.Fatalf("name=%q", definition.Name)
	}
	for _, raw := range []string{
		`{"queries":["What changed?"]}`,
		`{"queries":["primary sources","recent analysis","counter evidence"]}`,
	} {
		if err := action.ValidateInput(json.RawMessage(raw)); err != nil {
			t.Fatalf("ValidateInput(%s): %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{}`,
		`{"queries":[]}`,
		`{"queries":["same","SAME"]}`,
		`{"queries":[" leading"]}`,
		`{"queries":["a","b","c","d"]}`,
	} {
		if err := action.ValidateInput(json.RawMessage(raw)); err == nil {
			t.Fatalf("ValidateInput(%s) succeeded", raw)
		}
	}
}

func TestDiscoverSourcesActionIsAvailableOnlyToMaintainersWithProvider(t *testing.T) {
	available := NewDiscoverSourcesAction(&discoverSourcesBackendStub{}, nil).(ActionAvailability)
	for _, role := range []string{"owner", "editor"} {
		if ok, reason := available.Available(Execution{MemberRole: role}); !ok || reason != "" {
			t.Fatalf("role=%s available=%v reason=%q", role, ok, reason)
		}
	}
	if ok, reason := available.Available(Execution{MemberRole: "viewer"}); ok || reason != string(LeaderPolicyMembershipDenied) {
		t.Fatalf("viewer available=%v reason=%q", ok, reason)
	}
	unavailable := NewDiscoverSourcesAction(&discoverSourcesBackendStub{}, unavailableDiscoveryProvider{}).(ActionAvailability)
	if ok, reason := unavailable.Available(Execution{MemberRole: "owner"}); ok || reason != string(LeaderPolicyProviderUnavailable) {
		t.Fatalf("provider available=%v reason=%q", ok, reason)
	}
}

func TestDiscoverSourcesActionReturnsOnlyAggregateDiscoveryMetadata(t *testing.T) {
	backend := &discoverSourcesBackendStub{result: DiscoverSourcesResult{
		SessionID: "dss_safe", Status: "ready", NovelCandidateCount: 3,
		ExistingCandidateCount: 2, ExistingSelectedCount: 1,
	}}
	action := NewDiscoverSourcesAction(backend, nil)
	result, err := action.Execute(context.Background(), ActionRequest{
		ActionID: "decision:1/action:1", Input: json.RawMessage(`{"queries":["recent source","official source"]}`),
		UserID: "usr_1", ChatID: "chat_1", Attempt: Attempt{RunID: "run_1"},
	})
	if err != nil || result.Status != ActionSucceeded {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(backend.request.Queries, []string{"recent source", "official source"}) ||
		backend.request.RunID != "run_1" || backend.request.ActionID != "decision:1/action:1" ||
		backend.request.UserID != "usr_1" || backend.request.ChatID != "chat_1" {
		t.Fatalf("request=%+v", backend.request)
	}
	var output map[string]any
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"discovery_session_id", "status", "novel_candidate_count", "existing_candidate_count", "existing_selected_count"}
	if len(output) != len(wantKeys) {
		t.Fatalf("output=%s", result.Output)
	}
	for _, key := range wantKeys {
		if _, ok := output[key]; !ok {
			t.Fatalf("output missing %q: %s", key, result.Output)
		}
	}
}
