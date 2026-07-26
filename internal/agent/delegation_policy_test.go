package agent

import "testing"

func TestDelegationPolicySeparatesRequestedIntentFromEffectiveAuthority(t *testing.T) {
	requested := LeaderRouteDecision{Route: LeaderDelegateResearch, ReasonCode: LeaderReasonExplicitSourceDiscovery}
	allowed := EvaluateDelegationPolicy(requested, DelegationPolicyContext{
		MemberRole: "editor", NotebookAuthorized: true, RootActive: true, DeadlineValid: true,
		ProviderAvailable: true, RelationshipRegistered: true,
	})
	if allowed.RequestedRoute != LeaderDelegateResearch || allowed.EffectiveRoute != LeaderDelegateResearch || allowed.PolicyReason != LeaderPolicyAllowed {
		t.Fatalf("allowed=%+v", allowed)
	}
	denied := EvaluateDelegationPolicy(requested, DelegationPolicyContext{
		MemberRole: "viewer", NotebookAuthorized: true, RootActive: true, DeadlineValid: true,
		ProviderAvailable: true, RelationshipRegistered: true,
	})
	if denied.RequestedRoute != LeaderDelegateResearch || denied.EffectiveRoute != LeaderContinueChat || denied.IntentReason != LeaderReasonExplicitSourceDiscovery || denied.PolicyReason != LeaderPolicyMembershipDenied {
		t.Fatalf("denied=%+v", denied)
	}
}

func TestDelegationPolicyNeverEscalatesContinueChat(t *testing.T) {
	decision := LeaderRouteDecision{Route: LeaderContinueChat, ReasonCode: LeaderReasonExternalInformationWithoutDiscovery}
	result := EvaluateDelegationPolicy(decision, DelegationPolicyContext{
		MemberRole: "owner", NotebookAuthorized: true, RootActive: true, DeadlineValid: true,
		ProviderAvailable: true, RelationshipRegistered: true,
	})
	if result.EffectiveRoute != LeaderContinueChat || result.PolicyReason != LeaderPolicyContinueChat {
		t.Fatalf("result=%+v", result)
	}
}

func TestDelegationPolicyAppliesDeterministicDenialOrder(t *testing.T) {
	decision := LeaderRouteDecision{Route: LeaderDelegateResearch, ReasonCode: LeaderReasonExplicitSourceDiscovery}
	contexts := []struct {
		context DelegationPolicyContext
		want    LeaderPolicyReason
	}{
		{DelegationPolicyContext{MemberRole: "editor"}, LeaderPolicyNotebookAuthorityDenied},
		{DelegationPolicyContext{MemberRole: "editor", NotebookAuthorized: true}, LeaderPolicyRootInvalid},
		{DelegationPolicyContext{MemberRole: "editor", NotebookAuthorized: true, RootActive: true}, LeaderPolicyDeadlineExpired},
		{DelegationPolicyContext{MemberRole: "editor", NotebookAuthorized: true, RootActive: true, DeadlineValid: true}, LeaderPolicyProviderUnavailable},
		{DelegationPolicyContext{MemberRole: "editor", NotebookAuthorized: true, RootActive: true, DeadlineValid: true, ProviderAvailable: true}, LeaderPolicyRelationshipUnregistered},
		{DelegationPolicyContext{MemberRole: "editor", NotebookAuthorized: true, RootActive: true, DeadlineValid: true, ProviderAvailable: true, RelationshipRegistered: true, ExistingChildCount: 1}, LeaderPolicyChildLimit},
	}
	for _, test := range contexts {
		if got := EvaluateDelegationPolicy(decision, test.context).PolicyReason; got != test.want {
			t.Fatalf("context=%+v got=%q want=%q", test.context, got, test.want)
		}
	}
}
