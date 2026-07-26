package agent

import (
	"strings"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
)

func TestNewAgentPromptSetBindsExactlyFiveAgentPrompts(t *testing.T) {
	catalog := promptcatalog.MustLoadEmbedded()
	set, err := NewAgentPromptSet("nano-agent-prompts-v1", catalog, map[PromptPurpose]PromptVersionRef{
		PromptLeaderRouter:         {Identity: "agent.leader-router", Version: 1},
		PromptResearchPlanner:      {Identity: "agent.research-planner", Version: 1},
		PromptChatComposerBare:     {Identity: "agent.chat-composer-bare", Version: 1},
		PromptChatComposerGrounded: {Identity: "agent.chat-composer-grounded", Version: 1},
		PromptQueryContextualizer:  {Identity: "agent.query-contextualizer", Version: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Bindings) != 5 || len(set.SHA256) != 64 {
		t.Fatalf("set=%+v", set)
	}
	if set.Bindings[PromptLeaderRouter].Contract != "select_leader_route.v1" {
		t.Fatalf("router binding=%+v", set.Bindings[PromptLeaderRouter])
	}
}

func TestAgentConfigurationSetCarriesDistinctValidatedRoleProfiles(t *testing.T) {
	promptSet := mustTestPromptSet(t)
	set, err := NewAgentConfigurationSet("nano-agent-config-v1", promptSet, []AgentRoleProfile{
		{
			Role: RoleLeader, ExecutorVersion: "leader-executor-v1", Model: "leader-model",
			PromptPurposes: []PromptPurpose{PromptLeaderRouter, PromptChatComposerBare, PromptChatComposerGrounded, PromptQueryContextualizer},
			ToolAllowlist:  []string{"calculate", "current_time", "search_evidence"}, Run: RunConfig{ActionLimit: 8}, MaxAttempts: 3,
		},
		{
			Role: RoleResearch, ExecutorVersion: "research-executor-v1", Model: "research-model",
			PromptPurposes: []PromptPurpose{PromptResearchPlanner}, ToolAllowlist: []string{"web_search"}, Run: RunConfig{ActionLimit: 3}, MaxAttempts: 3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if set.Profiles[RoleLeader].Model == set.Profiles[RoleResearch].Model || len(set.SHA256) != 64 {
		t.Fatalf("set=%+v", set)
	}
	if set.Profiles[RoleResearch].Run.ActionLimit != 3 || set.Profiles[RoleResearch].ExecutorVersion != "research-executor-v1" {
		t.Fatalf("research=%+v", set.Profiles[RoleResearch])
	}
}

func TestAgentConfigurationRejectsMissingRoleUnknownPromptAndDuplicateTools(t *testing.T) {
	promptSet := mustTestPromptSet(t)
	_, err := NewAgentConfigurationSet("bad-config", promptSet, []AgentRoleProfile{{
		Role: RoleLeader, ExecutorVersion: "leader-v1", Model: "model",
		PromptPurposes: []PromptPurpose{PromptLeaderRouter}, ToolAllowlist: []string{"calculate", "calculate"}, MaxAttempts: 1,
	}})
	if err == nil {
		t.Fatal("accepted incomplete and duplicate configuration")
	}
	_, err = NewAgentPromptSet("bad-prompts", promptcatalog.MustLoadEmbedded(), map[PromptPurpose]PromptVersionRef{
		PromptLeaderRouter: {Identity: "agent.unknown", Version: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "Prompt") {
		t.Fatalf("err=%v", err)
	}
}

func mustTestPromptSet(t *testing.T) AgentPromptSet {
	t.Helper()
	set, err := DefaultAgentPromptSet(promptcatalog.MustLoadEmbedded())
	if err != nil {
		t.Fatal(err)
	}
	return set
}
