package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
)

type noopDefinitionExecutor struct{}

func (noopDefinitionExecutor) ExecuteAttempt(context.Context, Attempt) AttemptResolution {
	return AttemptResolution{Disposition: AttemptCompleted}
}

func TestExecutorRegistryResolvesExactDefinitionAndPolicyWithoutRole(t *testing.T) {
	registry := newTestExecutorRegistry(t)
	binding, err := registry.Resolve(agentcatalog.MustParseReference("chat.leader@1"))
	if err != nil {
		t.Fatal(err)
	}
	if binding.Definition.Reference().String() != "chat.leader@1" || binding.Definition.Executor != "chat_leader" {
		t.Fatalf("definition=%+v", binding.Definition)
	}
	if binding.ModelPolicy.Reference().String() != "agent.chat-default@1" || binding.Executor == nil {
		t.Fatalf("binding=%+v", binding)
	}
	if !binding.Capability.MemberVisible || !binding.Capability.CanPublish || binding.Capability.MaxChildren != 1 {
		t.Fatalf("code-owned capability=%+v", binding.Capability)
	}
	if _, err := registry.Resolve(agentcatalog.MustParseReference("chat.unknown@1")); err == nil || !strings.Contains(err.Error(), "unknown Agent Definition") {
		t.Fatalf("unknown definition err=%v", err)
	}
}

func TestExecutorRegistryRejectsDuplicateMissingAndCapabilityExpansion(t *testing.T) {
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prompts, err := promptcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	tools := productionToolCapabilities()
	registrations := productionExecutorRegistrations()
	for _, test := range []struct {
		name          string
		registrations []ExecutorRegistration
		want          string
	}{
		{"duplicate", append(registrations, registrations[0]), "duplicate Executor"},
		{"missing", registrations[:1], "unknown executor"},
		{"missing tool ceiling", []ExecutorRegistration{
			{Identity: "chat_leader", Executor: noopDefinitionExecutor{}, Capability: leaderExecutorCapability(map[string]bool{"current_time": true, "search_evidence": true})},
			registrations[1],
		}, "tool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewExecutorRegistry(catalog, prompts, tools, test.registrations...); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v want=%q", err, test.want)
			}
		})
	}
}

func newTestExecutorRegistry(t *testing.T) *ExecutorRegistry {
	t.Helper()
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	prompts, err := promptcatalog.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewExecutorRegistry(catalog, prompts, productionToolCapabilities(), productionExecutorRegistrations()...)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func productionToolCapabilities() map[string]agentcatalog.ToolCapability {
	return map[string]agentcatalog.ToolCapability{
		"calculate":       {Scheduling: agentcatalog.ToolOrderedSync},
		"current_time":    {Scheduling: agentcatalog.ToolOrderedSync},
		"search_evidence": {Scheduling: agentcatalog.ToolOrderedSync},
		"web_search":      {Scheduling: agentcatalog.ToolOrderedSync},
	}
}

func productionExecutorRegistrations() []ExecutorRegistration {
	return []ExecutorRegistration{
		{Identity: "chat_leader", Executor: noopDefinitionExecutor{}, Capability: leaderExecutorCapability(map[string]bool{
			"calculate": true, "current_time": true, "search_evidence": true,
		})},
		{Identity: "research", Executor: noopDefinitionExecutor{}, Capability: agentcatalog.ExecutorCapability{
			PromptPurposes: map[string]bool{"planner": true},
			Contracts: map[agentcatalog.Reference]bool{
				agentcatalog.MustParseReference("research.discovery-task@1"):   true,
				agentcatalog.MustParseReference("research.discovery-result@1"): true,
			},
			Tools: map[string]bool{"web_search": true},
			MaxLimits: agentcatalog.Limits{
				ModelCalls: 1, Actions: 1, ActionBatch: 1, ContextBytes: 65536, ResultBytes: 262144, Attempts: 3,
			},
		}},
	}
}

func leaderExecutorCapability(tools map[string]bool) agentcatalog.ExecutorCapability {
	return agentcatalog.ExecutorCapability{
		PromptPurposes: map[string]bool{
			"leader_router": true, "chat_composer_bare": true,
			"chat_composer_grounded": true, "query_contextualizer": true,
		},
		Contracts: map[agentcatalog.Reference]bool{
			agentcatalog.MustParseReference("chat.turn@1"):   true,
			agentcatalog.MustParseReference("chat.answer@1"): true,
		},
		Tools: tools, ChildExecutors: map[string]bool{"research": true},
		MaxLimits: agentcatalog.Limits{
			ModelCalls: 5, Actions: 8, ActionBatch: 4, ContextBytes: 65536, ResultBytes: 65536, Attempts: 3,
		},
		MaxChildren: 1, MemberVisible: true, CanPublish: true,
	}
}
