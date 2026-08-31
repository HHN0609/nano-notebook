package agentcatalog

import (
	"strings"
	"testing"
)

func TestEmbeddedQwenPlusContextPolicyDerivesPinnedBudgets(t *testing.T) {
	catalog, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	invocation, ok := catalog.ResolveModelPolicy(MustParseReference("agent.chat-default@1"))
	if !ok {
		t.Fatal("chat invocation policy is missing")
	}
	resolved, err := catalog.ResolveModelContextPolicy(invocation.Reference())
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Capability.ResolvedModel != "qwen-plus-2025-07-28" || resolved.Capability.ContextWindowTokens != 1_000_000 ||
		resolved.Capability.MaxInputTokens != 997_952 || resolved.Capability.MaxOutputTokens != 32_768 {
		t.Fatalf("capability=%+v", resolved.Capability)
	}
	if resolved.Budgets.HardInputTokens != 997_952 || resolved.Budgets.SafeInputTokens != 993_856 ||
		resolved.Budgets.CompactionTriggerTokens != 98_304 || resolved.Policy.KeepRecentTokens != 12_288 ||
		resolved.Policy.SummaryMaxOutputTokens != 2_048 || resolved.Policy.OverflowRetryLimit != 1 {
		t.Fatalf("resolved context=%+v", resolved)
	}
}

func TestEmbeddedDeepResearchPoliciesUseThinkingCapabilityOnly(t *testing.T) {
	catalog, err := LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"agent.deep-research-default@3", "agent.deep-research-default@4"} {
		policy, ok := catalog.ResolveModelPolicy(MustParseReference(reference))
		if !ok || policy.EnableThinking == nil || !*policy.EnableThinking {
			t.Fatalf("thinking policy %s=%+v ok=%v", reference, policy, ok)
		}
		resolved, err := catalog.ResolveModelContextPolicy(policy.Reference())
		if err != nil || resolved.Capability.InvocationMode != "thinking" || resolved.Capability.ResolvedModel != "qwen-plus-2025-07-28" {
			t.Fatalf("thinking context %s=%+v err=%v", reference, resolved, err)
		}
	}
	for _, reference := range []string{"agent.chat-default@1", "agent.research-default@1", "agent.studio-default@1"} {
		policy, ok := catalog.ResolveModelPolicy(MustParseReference(reference))
		if !ok || policy.EnableThinking != nil {
			t.Fatalf("non-thinking policy %s=%+v ok=%v", reference, policy, ok)
		}
		resolved, err := catalog.ResolveModelContextPolicy(policy.Reference())
		if err != nil || resolved.Capability.InvocationMode != "non_thinking" {
			t.Fatalf("non-thinking context %s=%+v err=%v", reference, resolved, err)
		}
	}
}

func TestCatalogRejectsContradictoryModelContextPolicy(t *testing.T) {
	files := minimalCatalogFS()
	files["provider-capabilities/provider.model.v1.json"] = mapFile(`{"identity":"provider.model","version":1,"provider_model":"provider/model","resolved_model":"model-1","context_window_tokens":1000,"max_input_tokens":900,"max_output_tokens":200,"tokenizer_identity":"test-tokenizer","tokenizer_version":"1","invocation_mode":"non_thinking"}`)
	files["model-context-policies/context.test.v1.json"] = mapFile(`{"identity":"context.test","version":1,"invocation_model_policy":"model.test@1","provider_capability":"provider.model@1","pinned_max_output_tokens":250,"soft_input_limit_tokens":700,"estimation_safety_tokens":100,"keep_recent_tokens":200,"summary_max_output_tokens":100,"overflow_retry_limit":1}`)
	_, err := LoadFS(files)
	if err == nil || !strings.Contains(err.Error(), "output") {
		t.Fatalf("err=%v", err)
	}
}

func TestCatalogRejectsThinkingModeMismatch(t *testing.T) {
	files := minimalCatalogFS()
	files["model-policies/model.test.v1.json"] = mapFile(`{"identity":"model.test","version":1,"provider_model":"provider/model","temperature":0,"max_output_tokens":128,"timeout_ms":1000,"enable_thinking":true}`)
	_, err := LoadFS(files)
	if err == nil || !strings.Contains(err.Error(), "invocation mode") {
		t.Fatalf("err=%v", err)
	}
}

func TestModelSelectionResolvesDifferentValidatedContextLimits(t *testing.T) {
	files := minimalCatalogFS()
	files["model-policies/model.large.v1.json"] = mapFile(`{"identity":"model.large","version":1,"provider_model":"provider/large","temperature":0,"max_output_tokens":256,"timeout_ms":1000,"enable_thinking":true}`)
	files["provider-capabilities/provider.large.v1.json"] = mapFile(`{"identity":"provider.large","version":1,"provider_model":"provider/large","resolved_model":"large-2026","context_window_tokens":4000,"max_input_tokens":3500,"max_output_tokens":500,"tokenizer_identity":"large-tokenizer","tokenizer_version":"2","invocation_mode":"thinking"}`)
	files["model-context-policies/context.large.v1.json"] = mapFile(`{"identity":"context.large","version":1,"invocation_model_policy":"model.large@1","provider_capability":"provider.large@1","pinned_max_output_tokens":256,"soft_input_limit_tokens":3000,"estimation_safety_tokens":200,"keep_recent_tokens":500,"summary_max_output_tokens":200,"overflow_retry_limit":2}`)
	catalog, err := LoadFS(files)
	if err != nil {
		t.Fatal(err)
	}
	small, err := catalog.ResolveModelContextPolicy(MustParseReference("model.test@1"))
	if err != nil {
		t.Fatal(err)
	}
	large, err := catalog.ResolveModelContextPolicy(MustParseReference("model.large@1"))
	if err != nil {
		t.Fatal(err)
	}
	if small.Budgets.CompactionTriggerTokens != 700 || large.Budgets.HardInputTokens != 3500 ||
		large.Budgets.SafeInputTokens != 3300 || large.Budgets.CompactionTriggerTokens != 3000 ||
		large.Policy.KeepRecentTokens != 500 || large.Capability.InvocationMode != "thinking" {
		t.Fatalf("small=%+v large=%+v", small, large)
	}
}
