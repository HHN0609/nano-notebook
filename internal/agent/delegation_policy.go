package agent

type LeaderPolicyReason string

const (
	LeaderPolicyContinueChat             LeaderPolicyReason = "continue_chat"
	LeaderPolicyAllowed                  LeaderPolicyReason = "delegation_allowed"
	LeaderPolicyMembershipDenied         LeaderPolicyReason = "membership_role_denied"
	LeaderPolicyNotebookAuthorityDenied  LeaderPolicyReason = "notebook_authority_denied"
	LeaderPolicyRootInvalid              LeaderPolicyReason = "root_run_invalid"
	LeaderPolicyDeadlineExpired          LeaderPolicyReason = "deadline_expired"
	LeaderPolicyProviderUnavailable      LeaderPolicyReason = "provider_unavailable"
	LeaderPolicyRelationshipUnregistered LeaderPolicyReason = "relationship_unregistered"
	LeaderPolicyChildLimit               LeaderPolicyReason = "research_child_limit"
)

type DelegationPolicyContext struct {
	MemberRole             string
	NotebookAuthorized     bool
	RootActive             bool
	DeadlineValid          bool
	ProviderAvailable      bool
	RelationshipRegistered bool
	ExistingChildCount     int
}

type LeaderRoutePolicy struct {
	RequestedRoute LeaderRoute
	EffectiveRoute LeaderRoute
	IntentReason   LeaderIntentReason
	PolicyReason   LeaderPolicyReason
}

func EvaluateDelegationPolicy(decision LeaderRouteDecision, context DelegationPolicyContext) LeaderRoutePolicy {
	result := LeaderRoutePolicy{
		RequestedRoute: decision.Route, EffectiveRoute: LeaderContinueChat,
		IntentReason: decision.ReasonCode, PolicyReason: LeaderPolicyContinueChat,
	}
	if decision.Route != LeaderDelegateResearch {
		return result
	}
	if context.MemberRole != "owner" && context.MemberRole != "editor" {
		result.PolicyReason = LeaderPolicyMembershipDenied
		return result
	}
	if !context.NotebookAuthorized {
		result.PolicyReason = LeaderPolicyNotebookAuthorityDenied
		return result
	}
	if !context.RootActive {
		result.PolicyReason = LeaderPolicyRootInvalid
		return result
	}
	if !context.DeadlineValid {
		result.PolicyReason = LeaderPolicyDeadlineExpired
		return result
	}
	if !context.ProviderAvailable {
		result.PolicyReason = LeaderPolicyProviderUnavailable
		return result
	}
	if !context.RelationshipRegistered {
		result.PolicyReason = LeaderPolicyRelationshipUnregistered
		return result
	}
	if context.ExistingChildCount != 0 {
		result.PolicyReason = LeaderPolicyChildLimit
		return result
	}
	result.EffectiveRoute = LeaderDelegateResearch
	result.PolicyReason = LeaderPolicyAllowed
	return result
}
