package agent

import (
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
)

type unavailableResearchProvider struct{}

func (unavailableResearchProvider) ResearchAvailable() bool { return false }

type availableResearchProvider struct{}

func (availableResearchProvider) ResearchAvailable() bool { return true }

func testDelegationAvailability(t *testing.T, provider ResearchProviderAvailability) *configuredDelegationAction {
	t.Helper()
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	root, ok := catalog.ResolveDefinition(agentcatalog.MustParseReference("chat.leader@1"))
	if !ok || len(root.Children) != 1 {
		t.Fatalf("chat leader root=%+v ok=%t", root, ok)
	}
	child, ok := catalog.ResolveDefinition(root.Children[0])
	if !ok {
		t.Fatalf("research child not found")
	}
	return &configuredDelegationAction{catalog: catalog, child: child, provider: provider}
}

func TestConfiguredDelegationAvailableAppliesPolicyReasons(t *testing.T) {
	action := testDelegationAvailability(t, availableResearchProvider{})
	tests := []struct {
		name      string
		execution Execution
		ok        bool
		reason    string
	}{
		{name: "allowed owner", execution: Execution{AgentConfigID: "chat.leader@1", MemberRole: "owner"}, ok: true},
		{name: "allowed editor", execution: Execution{AgentConfigID: "chat.leader@1", MemberRole: "editor"}, ok: true},
		{name: "membership denied", execution: Execution{AgentConfigID: "chat.leader@1", MemberRole: "viewer"}, reason: string(LeaderPolicyMembershipDenied)},
		{name: "child limit", execution: Execution{AgentConfigID: "chat.leader@1", MemberRole: "owner", ExistingChildCount: 1}, reason: string(LeaderPolicyChildLimit)},
		{name: "relationship unregistered", execution: Execution{AgentConfigID: "studio.report@1", MemberRole: "owner"}, reason: string(LeaderPolicyRelationshipUnregistered)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := action.Available(tt.execution)
			if ok != tt.ok || reason != tt.reason {
				t.Fatalf("ok=%t reason=%q want ok=%t reason=%q", ok, reason, tt.ok, tt.reason)
			}
		})
	}
}

func TestConfiguredDelegationAvailableRequiresResearchProvider(t *testing.T) {
	action := testDelegationAvailability(t, unavailableResearchProvider{})
	ok, reason := action.Available(Execution{AgentConfigID: "chat.leader@1", MemberRole: "owner"})
	if ok || reason != string(LeaderPolicyProviderUnavailable) {
		t.Fatalf("ok=%t reason=%q", ok, reason)
	}
}
