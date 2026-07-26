package agent

import (
	"context"
	"testing"
)

type noopRoleExecutor struct{}

func (noopRoleExecutor) ExecuteAttempt(context.Context, Attempt) AttemptResolution {
	return AttemptResolution{Disposition: AttemptCompleted}
}

func TestRoleRegistryAcceptsOnlyFixedRolesAndTopology(t *testing.T) {
	registry, err := NewRoleRegistry(
		RoleRegistration{Role: RoleLeader, ExecutorVersion: "leader-v1", Executor: noopRoleExecutor{}},
		RoleRegistration{Role: RoleResearch, ExecutorVersion: "research-v1", Executor: noopRoleExecutor{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(RoleLeader, "leader-v1"); err != nil {
		t.Fatal(err)
	}
	if err := registry.AuthorizeDelegation(RoleLeader, RoleResearch, 0, 1); err != nil {
		t.Fatal(err)
	}
	for _, edge := range []struct {
		parent, child  AgentRole
		ordinal, depth int
	}{
		{RoleResearch, RoleLeader, 0, 1},
		{RoleResearch, RoleResearch, 0, 2},
		{RoleLeader, RoleResearch, 1, 1},
		{RoleLeader, RoleResearch, 0, 2},
	} {
		if err := registry.AuthorizeDelegation(edge.parent, edge.child, edge.ordinal, edge.depth); err == nil {
			t.Fatalf("accepted edge=%+v", edge)
		}
	}
}

func TestRoleRegistryRejectsDuplicateUnknownAndMismatchedExecutor(t *testing.T) {
	tests := [][]RoleRegistration{
		{{Role: RoleLeader, ExecutorVersion: "v1", Executor: noopRoleExecutor{}}, {Role: RoleLeader, ExecutorVersion: "v1", Executor: noopRoleExecutor{}}},
		{{Role: AgentRole("critic"), ExecutorVersion: "v1", Executor: noopRoleExecutor{}}},
		{{Role: RoleLeader, ExecutorVersion: "", Executor: noopRoleExecutor{}}},
	}
	for _, registrations := range tests {
		if _, err := NewRoleRegistry(registrations...); err == nil {
			t.Fatalf("accepted registrations=%+v", registrations)
		}
	}
}

func TestRoleCapabilitiesAreCodeOwned(t *testing.T) {
	if !RoleLeader.MemberVisible() || !RoleLeader.CanPublish() || !RoleLeader.CanDelegate() {
		t.Fatal("Leader capabilities changed")
	}
	if RoleResearch.MemberVisible() || RoleResearch.CanPublish() || RoleResearch.CanDelegate() {
		t.Fatal("Research gained forbidden capabilities")
	}
}
