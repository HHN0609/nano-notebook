package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
)

type DefinitionExecutor interface {
	ExecuteAttempt(context.Context, Attempt) AttemptResolution
}

type ExecutorRegistration struct {
	Identity   string
	Executor   DefinitionExecutor
	Capability agentcatalog.ExecutorCapability
}

type ResolvedExecution struct {
	Definition   agentcatalog.Definition
	ModelPolicy  agentcatalog.ModelPolicy
	ModelContext agentcatalog.ResolvedModelContextPolicy
	Executor     DefinitionExecutor
	Capability   agentcatalog.ExecutorCapability
}

type ExecutorRegistry struct {
	catalog       agentcatalog.Catalog
	registrations map[string]ExecutorRegistration
}

func NewExecutorRegistry(catalog agentcatalog.Catalog, prompts promptcatalog.Catalog, tools map[string]agentcatalog.ToolCapability, registrations ...ExecutorRegistration) (*ExecutorRegistry, error) {
	registry := &ExecutorRegistry{catalog: catalog, registrations: make(map[string]ExecutorRegistration, len(registrations))}
	bindings := agentcatalog.Bindings{
		Prompts:   make(map[agentcatalog.Reference]bool),
		Tools:     cloneToolCapabilities(tools),
		Executors: make(map[string]agentcatalog.ExecutorCapability, len(registrations)),
	}
	for _, prompt := range prompts.Versions() {
		bindings.Prompts[agentcatalog.Reference{Identity: prompt.Identity, Version: prompt.Version}] = true
	}
	for _, registration := range registrations {
		registration.Identity = strings.TrimSpace(registration.Identity)
		if !validExecutorIdentity(registration.Identity) || registration.Executor == nil {
			return nil, errors.New("invalid Executor registration")
		}
		if _, duplicate := registry.registrations[registration.Identity]; duplicate {
			return nil, fmt.Errorf("duplicate Executor registration %q", registration.Identity)
		}
		registration.Capability = cloneExecutorCapability(registration.Capability)
		registry.registrations[registration.Identity] = registration
		bindings.Executors[registration.Identity] = registration.Capability
	}
	if err := catalog.ValidateBindings(bindings); err != nil {
		return nil, err
	}
	for identity := range registry.registrations {
		used := false
		for _, definition := range catalog.Definitions() {
			if definition.Executor == identity {
				used = true
				break
			}
		}
		if !used {
			return nil, fmt.Errorf("Executor registration %q has no Agent Definition", identity)
		}
	}
	return registry, nil
}

func NewNanoExecutorRegistry(catalog agentcatalog.Catalog, prompts promptcatalog.Catalog, chatLeader, research DefinitionExecutor, studio ...DefinitionExecutor) (*ExecutorRegistry, error) {
	studioExecutor := DefinitionExecutor(unavailableStudioExecutor{})
	if len(studio) > 0 && studio[0] != nil {
		studioExecutor = studio[0]
	}
	return NewExecutorRegistry(catalog, prompts, NanoToolCapabilities(),
		ExecutorRegistration{Identity: "chat_leader", Executor: chatLeader, Capability: ChatLeaderExecutorCapability()},
		ExecutorRegistration{Identity: "research", Executor: research, Capability: ResearchExecutorCapability()},
		ExecutorRegistration{Identity: "studio_structured_output", Executor: studioExecutor, Capability: StudioStructuredOutputExecutorCapability()},
	)
}

type unavailableStudioExecutor struct{}

func (unavailableStudioExecutor) ExecuteAttempt(context.Context, Attempt) AttemptResolution {
	return AttemptResolution{Disposition: AttemptTerminal, ErrorCode: "studio_executor_unavailable"}
}

func (r *ExecutorRegistry) Resolve(reference agentcatalog.Reference) (ResolvedExecution, error) {
	if r == nil {
		return ResolvedExecution{}, errors.New("Executor Registry is nil")
	}
	definition, ok := r.catalog.ResolveDefinition(reference)
	if !ok {
		return ResolvedExecution{}, fmt.Errorf("unknown Agent Definition %s", reference)
	}
	registration, ok := r.registrations[definition.Executor]
	if !ok {
		return ResolvedExecution{}, fmt.Errorf("Agent Definition %s has no Executor", reference)
	}
	policy, ok := r.catalog.ResolveModelPolicy(definition.ModelPolicy)
	if !ok {
		return ResolvedExecution{}, fmt.Errorf("Agent Definition %s has no Model Policy", reference)
	}
	modelContext, err := r.catalog.ResolveModelContextPolicy(policy.Reference())
	if err != nil {
		return ResolvedExecution{}, err
	}
	return ResolvedExecution{
		Definition: definition, ModelPolicy: policy, ModelContext: modelContext, Executor: registration.Executor,
		Capability: cloneExecutorCapability(registration.Capability),
	}, nil
}

// NanoToolCapabilities declares each tool's execution scheduling. calculate,
// current_time, and search_evidence are read-only and side-effect-free, so
// a batch made up only of these can run concurrently. web_search stays
// ordered_sync: it calls an external, rate-limited provider, and batching
// concurrent calls to it needs its own rate-limit accounting first.
func NanoToolCapabilities() map[string]agentcatalog.ToolCapability {
	return map[string]agentcatalog.ToolCapability{
		"calculate":       {Scheduling: agentcatalog.ToolParallel},
		"current_time":    {Scheduling: agentcatalog.ToolParallel},
		"search_evidence": {Scheduling: agentcatalog.ToolParallel},
		"web_search":      {Scheduling: agentcatalog.ToolOrderedSync},
	}
}

func ChatLeaderExecutorCapability() agentcatalog.ExecutorCapability {
	return agentcatalog.ExecutorCapability{
		PromptPurposes: map[string]bool{
			"leader_router": true, "chat_composer_bare": true,
			"chat_composer_grounded": true, "query_contextualizer": true,
		},
		Contracts: map[agentcatalog.Reference]bool{
			agentcatalog.MustParseReference("chat.turn@1"):   true,
			agentcatalog.MustParseReference("chat.answer@1"): true,
		},
		Tools: map[string]bool{
			"calculate": true, "current_time": true, "search_evidence": true,
		},
		ChildExecutors: map[string]bool{"research": true},
		MaxLimits: agentcatalog.Limits{
			ModelCalls: 5, Actions: 8, ActionBatch: 4, ContextBytes: 65536, ResultBytes: 65536, Attempts: 3,
		},
		MaxChildren: 1, MemberVisible: true, CanPublish: true,
	}
}

func ResearchExecutorCapability() agentcatalog.ExecutorCapability {
	return agentcatalog.ExecutorCapability{
		PromptPurposes: map[string]bool{"planner": true},
		Contracts: map[agentcatalog.Reference]bool{
			agentcatalog.MustParseReference("research.discovery-task@1"):   true,
			agentcatalog.MustParseReference("research.discovery-result@1"): true,
		},
		Tools: map[string]bool{"web_search": true},
		MaxLimits: agentcatalog.Limits{
			ModelCalls: 1, Actions: 1, ActionBatch: 1, ContextBytes: 65536, ResultBytes: 262144, Attempts: 3,
		},
	}
}

func StudioStructuredOutputExecutorCapability() agentcatalog.ExecutorCapability {
	return agentcatalog.ExecutorCapability{
		PromptPurposes: map[string]bool{
			"report": true, "flashcards": true, "mind_map": true, "data_table": true,
		},
		Contracts: map[agentcatalog.Reference]bool{
			agentcatalog.MustParseReference("studio.output-request@1"):    true,
			agentcatalog.MustParseReference("studio.report-result@1"):     true,
			agentcatalog.MustParseReference("studio.flashcards-result@1"): true,
			agentcatalog.MustParseReference("studio.mind-map-result@1"):   true,
			agentcatalog.MustParseReference("studio.data-table-result@1"): true,
		},
		Tools: map[string]bool{"search_evidence": true},
		MaxLimits: agentcatalog.Limits{
			ModelCalls: 2, Actions: 1, ActionBatch: 1, ContextBytes: 65536, ResultBytes: 65536, Attempts: 3,
		},
		MemberVisible: true, CanPublish: true,
	}
}

func validExecutorIdentity(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if unicode.IsLower(character) || unicode.IsDigit(character) || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func cloneToolCapabilities(source map[string]agentcatalog.ToolCapability) map[string]agentcatalog.ToolCapability {
	cloned := make(map[string]agentcatalog.ToolCapability, len(source))
	for name, capability := range source {
		cloned[name] = capability
	}
	return cloned
}

func cloneExecutorCapability(source agentcatalog.ExecutorCapability) agentcatalog.ExecutorCapability {
	source.PromptPurposes = cloneBoolMap(source.PromptPurposes)
	source.Contracts = cloneBoolMap(source.Contracts)
	source.Tools = cloneBoolMap(source.Tools)
	source.ChildExecutors = cloneBoolMap(source.ChildExecutors)
	return source
}

func cloneBoolMap[K comparable](source map[K]bool) map[K]bool {
	if source == nil {
		return nil
	}
	cloned := make(map[K]bool, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
