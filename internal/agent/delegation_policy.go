package agent

type LeaderPolicyReason string

const (
	LeaderPolicyMembershipDenied         LeaderPolicyReason = "membership_role_denied"
	LeaderPolicyProviderUnavailable      LeaderPolicyReason = "provider_unavailable"
	LeaderPolicyRelationshipUnregistered LeaderPolicyReason = "relationship_unregistered"
	LeaderPolicyChildLimit               LeaderPolicyReason = "research_child_limit"
)
