package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type RoleExecutor interface {
	ExecuteAttempt(context.Context, Attempt) AttemptResolution
}

type AgentRole string

const (
	RoleLeader   AgentRole = "leader"
	RoleResearch AgentRole = "research"
)

type RoleRegistration struct {
	Role            AgentRole
	ExecutorVersion string
	Executor        RoleExecutor
}

type RoleRegistry struct {
	registrations map[AgentRole]RoleRegistration
}

func NewRoleRegistry(registrations ...RoleRegistration) (*RoleRegistry, error) {
	registry := &RoleRegistry{registrations: make(map[AgentRole]RoleRegistration, len(registrations))}
	for _, registration := range registrations {
		registration.ExecutorVersion = strings.TrimSpace(registration.ExecutorVersion)
		if registration.Role != RoleLeader && registration.Role != RoleResearch || registration.ExecutorVersion == "" || registration.Executor == nil {
			return nil, errors.New("invalid Role registration")
		}
		if _, duplicate := registry.registrations[registration.Role]; duplicate {
			return nil, fmt.Errorf("duplicate Role registration %q", registration.Role)
		}
		registry.registrations[registration.Role] = registration
	}
	if len(registry.registrations) != 2 {
		return nil, errors.New("Leader and Research Role registrations are required")
	}
	return registry, nil
}

func (r *RoleRegistry) Resolve(role AgentRole, executorVersion string) (RoleExecutor, error) {
	if r == nil {
		return nil, errors.New("Role Registry is nil")
	}
	registration, ok := r.registrations[role]
	if !ok || registration.ExecutorVersion != strings.TrimSpace(executorVersion) {
		return nil, fmt.Errorf("unsupported Role executor %s@%s", role, executorVersion)
	}
	return registration.Executor, nil
}

func (r *RoleRegistry) AuthorizeDelegation(parent, child AgentRole, ordinal, depth int) error {
	if r == nil || parent != RoleLeader || child != RoleResearch || ordinal != 0 || depth != 1 {
		return errors.New("illegal Agent delegation topology")
	}
	return nil
}

func (r AgentRole) MemberVisible() bool { return r == RoleLeader }
func (r AgentRole) CanPublish() bool    { return r == RoleLeader }
func (r AgentRole) CanDelegate() bool   { return r == RoleLeader }
