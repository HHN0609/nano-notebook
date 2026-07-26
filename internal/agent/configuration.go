package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
)

type PromptPurpose string

const (
	PromptLeaderRouter         PromptPurpose = "leader_router"
	PromptResearchPlanner      PromptPurpose = "research_planner"
	PromptChatComposerBare     PromptPurpose = "chat_composer_bare"
	PromptChatComposerGrounded PromptPurpose = "chat_composer_grounded"
	PromptQueryContextualizer  PromptPurpose = "query_contextualizer"
)

var requiredPromptIdentities = map[PromptPurpose]string{
	PromptLeaderRouter:         "agent.leader-router",
	PromptResearchPlanner:      "agent.research-planner",
	PromptChatComposerBare:     "agent.chat-composer-bare",
	PromptChatComposerGrounded: "agent.chat-composer-grounded",
	PromptQueryContextualizer:  "agent.query-contextualizer",
}

type PromptVersionRef struct {
	Identity string `json:"identity"`
	Version  int    `json:"version"`
	Contract string `json:"contract"`
	SHA256   string `json:"sha256"`
}

type AgentPromptSet struct {
	ID       string                             `json:"id"`
	Bindings map[PromptPurpose]PromptVersionRef `json:"bindings"`
	SHA256   string                             `json:"sha256"`
}

type AgentRoleProfile struct {
	Role            AgentRole       `json:"role"`
	ExecutorVersion string          `json:"executor_version"`
	Model           string          `json:"model"`
	PromptPurposes  []PromptPurpose `json:"prompt_purposes"`
	ToolAllowlist   []string        `json:"tool_allowlist"`
	Run             RunConfig       `json:"run"`
	MaxAttempts     int             `json:"max_attempts"`
}

type AgentConfigurationSet struct {
	ID            string                         `json:"id"`
	PromptSetID   string                         `json:"prompt_set_id"`
	PromptSetHash string                         `json:"prompt_set_hash"`
	Profiles      map[AgentRole]AgentRoleProfile `json:"profiles"`
	SHA256        string                         `json:"sha256"`
}

func DefaultAgentPromptSet(catalog promptcatalog.Catalog) (AgentPromptSet, error) {
	references := make(map[PromptPurpose]PromptVersionRef, len(requiredPromptIdentities))
	for purpose, identity := range requiredPromptIdentities {
		references[purpose] = PromptVersionRef{Identity: identity, Version: 1}
	}
	return NewAgentPromptSet("nano-agent-prompts-v1", catalog, references)
}

func NewAgentPromptSet(id string, catalog promptcatalog.Catalog, references map[PromptPurpose]PromptVersionRef) (AgentPromptSet, error) {
	id = strings.TrimSpace(id)
	if id == "" || len(references) != len(requiredPromptIdentities) {
		return AgentPromptSet{}, errors.New("Agent Prompt Set is incomplete")
	}
	set := AgentPromptSet{ID: id, Bindings: make(map[PromptPurpose]PromptVersionRef, len(references))}
	for purpose, expectedIdentity := range requiredPromptIdentities {
		reference, ok := references[purpose]
		if !ok || strings.TrimSpace(reference.Identity) != expectedIdentity || reference.Version < 1 {
			return AgentPromptSet{}, fmt.Errorf("Prompt binding %s is invalid", purpose)
		}
		prompt, ok := catalog.Resolve(reference.Identity, reference.Version)
		if !ok {
			return AgentPromptSet{}, fmt.Errorf("Prompt %s@%d is not registered", reference.Identity, reference.Version)
		}
		set.Bindings[purpose] = PromptVersionRef{Identity: prompt.Identity, Version: prompt.Version, Contract: prompt.Contract, SHA256: prompt.SHA256}
	}
	hash, err := hashPromptSet(set)
	if err != nil {
		return AgentPromptSet{}, err
	}
	set.SHA256 = hash
	return set, nil
}

func NewAgentConfigurationSet(id string, promptSet AgentPromptSet, profiles []AgentRoleProfile) (AgentConfigurationSet, error) {
	id = strings.TrimSpace(id)
	if id == "" || promptSet.ID == "" || len(promptSet.SHA256) != 64 || len(profiles) != 2 {
		return AgentConfigurationSet{}, errors.New("Agent Configuration Set is incomplete")
	}
	set := AgentConfigurationSet{
		ID: id, PromptSetID: promptSet.ID, PromptSetHash: promptSet.SHA256,
		Profiles: make(map[AgentRole]AgentRoleProfile, 2),
	}
	for _, profile := range profiles {
		profile.ExecutorVersion = strings.TrimSpace(profile.ExecutorVersion)
		profile.Model = strings.TrimSpace(profile.Model)
		if profile.Role != RoleLeader && profile.Role != RoleResearch || profile.ExecutorVersion == "" || profile.Model == "" || profile.MaxAttempts < 1 || profile.MaxAttempts > 10 {
			return AgentConfigurationSet{}, errors.New("Agent Role Profile is invalid")
		}
		if _, duplicate := set.Profiles[profile.Role]; duplicate {
			return AgentConfigurationSet{}, errors.New("duplicate Agent Role Profile")
		}
		if !validProfilePrompts(profile.Role, profile.PromptPurposes) || !validProfileTools(profile.Role, profile.ToolAllowlist) || profile.Run.ActionLimit < 1 {
			return AgentConfigurationSet{}, errors.New("Agent Role Profile bindings are invalid")
		}
		profile.PromptPurposes = sortedPromptPurposes(profile.PromptPurposes)
		profile.ToolAllowlist = sortedUniqueStrings(profile.ToolAllowlist)
		set.Profiles[profile.Role] = profile
	}
	if _, ok := set.Profiles[RoleLeader]; !ok {
		return AgentConfigurationSet{}, errors.New("Leader Role Profile is missing")
	}
	if _, ok := set.Profiles[RoleResearch]; !ok {
		return AgentConfigurationSet{}, errors.New("Research Role Profile is missing")
	}
	hash, err := hashConfigurationSet(set)
	if err != nil {
		return AgentConfigurationSet{}, err
	}
	set.SHA256 = hash
	return set, nil
}

func DefaultAgentConfigurationSet(id, leaderModel, researchModel string, run RunConfig) (AgentConfigurationSet, error) {
	prompts, err := DefaultAgentPromptSet(promptcatalog.MustLoadEmbedded())
	if err != nil {
		return AgentConfigurationSet{}, err
	}
	if strings.TrimSpace(researchModel) == "" {
		researchModel = leaderModel
	}
	researchRun := run
	if researchRun.ActionLimit < 1 || researchRun.ActionLimit > 3 {
		researchRun.ActionLimit = 3
	}
	return NewAgentConfigurationSet(id, prompts, []AgentRoleProfile{
		{Role: RoleLeader, ExecutorVersion: "leader-executor-v1", Model: leaderModel,
			PromptPurposes: []PromptPurpose{PromptLeaderRouter, PromptChatComposerBare, PromptChatComposerGrounded, PromptQueryContextualizer},
			ToolAllowlist:  []string{"calculate", "current_time", "search_evidence"}, Run: run, MaxAttempts: 3},
		{Role: RoleResearch, ExecutorVersion: "research-executor-v1", Model: researchModel,
			PromptPurposes: []PromptPurpose{PromptResearchPlanner}, ToolAllowlist: []string{"web_search"}, Run: researchRun, MaxAttempts: 3},
	})
}

func DefaultAgentConfigurationBundle(id, leaderModel, researchModel string, run RunConfig) (AgentPromptSet, AgentConfigurationSet, error) {
	prompts, err := DefaultAgentPromptSet(promptcatalog.MustLoadEmbedded())
	if err != nil {
		return AgentPromptSet{}, AgentConfigurationSet{}, err
	}
	configuration, err := DefaultAgentConfigurationSet(id, leaderModel, researchModel, run)
	return prompts, configuration, err
}

func validProfilePrompts(role AgentRole, purposes []PromptPurpose) bool {
	want := map[AgentRole]map[PromptPurpose]bool{
		RoleLeader:   {PromptLeaderRouter: true, PromptChatComposerBare: true, PromptChatComposerGrounded: true, PromptQueryContextualizer: true},
		RoleResearch: {PromptResearchPlanner: true},
	}[role]
	if len(purposes) != len(want) {
		return false
	}
	seen := make(map[PromptPurpose]bool, len(purposes))
	for _, purpose := range purposes {
		if !want[purpose] || seen[purpose] {
			return false
		}
		seen[purpose] = true
	}
	return true
}

func validProfileTools(role AgentRole, tools []string) bool {
	allowed := map[AgentRole]map[string]bool{
		RoleLeader:   {"calculate": true, "current_time": true, "search_evidence": true},
		RoleResearch: {"web_search": true},
	}[role]
	if len(tools) == 0 {
		return false
	}
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if !allowed[tool] || seen[tool] {
			return false
		}
		seen[tool] = true
	}
	return true
}

func sortedPromptPurposes(values []PromptPurpose) []PromptPurpose {
	result := append([]PromptPurpose(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func sortedUniqueStrings(values []string) []string {
	result := append([]string(nil), values...)
	for index := range result {
		result[index] = strings.TrimSpace(result[index])
	}
	sort.Strings(result)
	return result
}

func hashPromptSet(set AgentPromptSet) (string, error) {
	type binding struct {
		Purpose PromptPurpose    `json:"purpose"`
		Prompt  PromptVersionRef `json:"prompt"`
	}
	bindings := make([]binding, 0, len(set.Bindings))
	for purpose, prompt := range set.Bindings {
		bindings = append(bindings, binding{Purpose: purpose, Prompt: prompt})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Purpose < bindings[j].Purpose })
	return canonicalHash(struct {
		ID       string    `json:"id"`
		Bindings []binding `json:"bindings"`
	}{set.ID, bindings})
}

func hashConfigurationSet(set AgentConfigurationSet) (string, error) {
	profiles := []AgentRoleProfile{set.Profiles[RoleLeader], set.Profiles[RoleResearch]}
	return canonicalHash(struct {
		ID            string             `json:"id"`
		PromptSetID   string             `json:"prompt_set_id"`
		PromptSetHash string             `json:"prompt_set_hash"`
		Profiles      []AgentRoleProfile `json:"profiles"`
	}{set.ID, set.PromptSetID, set.PromptSetHash, profiles})
}

func canonicalHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func RoleProfileSHA256(profile AgentRoleProfile) (string, error) {
	return canonicalHash(profile)
}
