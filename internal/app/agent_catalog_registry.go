package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/skillcatalog"
	"github.com/jackc/pgx/v5"
)

type registeredCatalogEntry struct {
	SHA256     string
	Payload    []byte
	SourcePath string
}

func registerEmbeddedAgentCatalog(ctx context.Context, db *DB) error {
	skills, err := skillcatalog.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load embedded Skill Catalog: %w", err)
	}
	if err := RegisterSkillCatalog(ctx, db, skills); err != nil {
		return err
	}
	catalog, err := agentcatalog.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load embedded Agent Catalog: %w", err)
	}
	return RegisterAgentCatalog(ctx, db, catalog)
}

func RegisterSkillCatalog(ctx context.Context, db *DB, catalog skillcatalog.Catalog) error {
	if db == nil || db.pool == nil {
		return errors.New("nil database")
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, skill := range catalog.Versions() {
		payload, err := json.Marshal(skill)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_skill_versions(
				skill_identity,skill_version,canonical_sha256,name,description,body,canonical_payload,source_path
			) values($1,$2,$3,$4,$5,$6,$7::jsonb,$8)
			on conflict(skill_identity,skill_version) do nothing
		`, skill.Identity, skill.Version, skill.SHA256, skill.Name, skill.Description, skill.Body, string(payload), skill.SourcePath); err != nil {
			return fmt.Errorf("register skill %s@%d: %w", skill.Identity, skill.Version, err)
		}
		reference := agentcatalog.Reference{Identity: skill.Identity, Version: skill.Version}
		if err := verifyCatalogEntry(ctx, tx, "agent_skill_versions", "skill_identity", "skill_version", reference, skill.SHA256, payload, skill.SourcePath, "skill"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func VerifySkillCatalogReady(ctx context.Context, db *DB, catalog skillcatalog.Catalog) error {
	if db == nil || db.pool == nil {
		return errors.New("nil database")
	}
	for _, skill := range catalog.Versions() {
		payload, _ := json.Marshal(skill)
		reference := agentcatalog.Reference{Identity: skill.Identity, Version: skill.Version}
		if err := verifyCatalogEntry(ctx, db.pool, "agent_skill_versions", "skill_identity", "skill_version", reference, skill.SHA256, payload, skill.SourcePath, "skill"); err != nil {
			return err
		}
	}
	return nil
}

func RegisterAgentCatalog(ctx context.Context, db *DB, catalog agentcatalog.Catalog) error {
	if db == nil || db.pool == nil {
		return errors.New("nil database")
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, contract := range catalog.Contracts() {
		payload, err := json.Marshal(contract)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_contract_versions(
				contract_identity,contract_version,canonical_sha256,json_schema,canonical_payload,source_path
			) values($1,$2,$3,$4::jsonb,$5::jsonb,$6)
			on conflict(contract_identity,contract_version) do nothing
		`, contract.Identity, contract.Version, contract.SHA256, string(contract.Schema), string(payload), contract.SourcePath); err != nil {
			return fmt.Errorf("register contract %s: %w", contract.Reference(), err)
		}
		if err := verifyCatalogEntry(ctx, tx, "agent_contract_versions", "contract_identity", "contract_version", contract.Reference(), contract.SHA256, payload, contract.SourcePath, "contract"); err != nil {
			return err
		}
	}
	for _, policy := range catalog.ModelPolicies() {
		payload, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_model_policy_versions(
				policy_identity,policy_version,canonical_sha256,provider_model,temperature,max_output_tokens,timeout_ms,canonical_payload,source_path
			) values($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
			on conflict(policy_identity,policy_version) do nothing
		`, policy.Identity, policy.Version, policy.SHA256, policy.ProviderModel, policy.Temperature, policy.MaxOutputTokens, policy.TimeoutMS, string(payload), policy.SourcePath); err != nil {
			return fmt.Errorf("register model policy %s: %w", policy.Reference(), err)
		}
		if err := verifyCatalogEntry(ctx, tx, "agent_model_policy_versions", "policy_identity", "policy_version", policy.Reference(), policy.SHA256, payload, policy.SourcePath, "model policy"); err != nil {
			return err
		}
	}
	for _, capability := range catalog.ProviderCapabilities() {
		payload, err := json.Marshal(capability)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into provider_model_capability_versions(
				capability_identity,capability_version,canonical_sha256,provider_model,resolved_model,
				context_window_tokens,max_input_tokens,max_output_tokens,tokenizer_identity,tokenizer_version,
				invocation_mode,canonical_payload,source_path
			) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13)
			on conflict(capability_identity,capability_version) do nothing
		`, capability.Identity, capability.Version, capability.SHA256, capability.ProviderModel, capability.ResolvedModel,
			capability.ContextWindowTokens, capability.MaxInputTokens, capability.MaxOutputTokens,
			capability.TokenizerIdentity, capability.TokenizerVersion, capability.InvocationMode,
			string(payload), capability.SourcePath); err != nil {
			return fmt.Errorf("register provider capability %s: %w", capability.Reference(), err)
		}
		if err := verifyCatalogEntry(ctx, tx, "provider_model_capability_versions", "capability_identity", "capability_version", capability.Reference(), capability.SHA256, payload, capability.SourcePath, "provider capability"); err != nil {
			return err
		}
	}
	for _, policy := range catalog.ModelContextPolicies() {
		payload, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_model_context_policy_versions(
				context_policy_identity,context_policy_version,canonical_sha256,
				model_policy_identity,model_policy_version,capability_identity,capability_version,
				pinned_max_output_tokens,soft_input_limit_tokens,estimation_safety_tokens,
				keep_recent_tokens,summary_max_output_tokens,overflow_retry_limit,canonical_payload,source_path
			) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15)
			on conflict(context_policy_identity,context_policy_version) do nothing
		`, policy.Identity, policy.Version, policy.SHA256,
			policy.InvocationModelPolicy.Identity, policy.InvocationModelPolicy.Version,
			policy.ProviderCapability.Identity, policy.ProviderCapability.Version,
			policy.PinnedMaxOutputTokens, policy.SoftInputLimitTokens, policy.EstimationSafetyTokens,
			policy.KeepRecentTokens, policy.SummaryMaxOutputTokens, policy.OverflowRetryLimit,
			string(payload), policy.SourcePath); err != nil {
			return fmt.Errorf("register model context policy %s: %w", policy.Reference(), err)
		}
		if err := verifyCatalogEntry(ctx, tx, "agent_model_context_policy_versions", "context_policy_identity", "context_policy_version", policy.Reference(), policy.SHA256, payload, policy.SourcePath, "model context policy"); err != nil {
			return err
		}
	}
	for _, definition := range catalog.Definitions() {
		payload, err := json.Marshal(definition)
		if err != nil {
			return err
		}
		prompts, _ := json.Marshal(definition.Prompts)
		skillAllowlist := definition.Skills
		if skillAllowlist == nil {
			skillAllowlist = []agentcatalog.Reference{}
		}
		skills, _ := json.Marshal(skillAllowlist)
		tools, _ := json.Marshal(definition.Tools)
		children, _ := json.Marshal(definition.Children)
		limits, _ := json.Marshal(definition.Limits)
		var delegation any
		if definition.Delegation != nil {
			encoded, marshalErr := json.Marshal(definition.Delegation)
			if marshalErr != nil {
				return marshalErr
			}
			delegation = string(encoded)
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_definition_versions(
				definition_identity,definition_version,canonical_sha256,executor,
				model_policy_identity,model_policy_version,prompt_bindings,
				input_contract_identity,input_contract_version,result_contract_identity,result_contract_version,
				skill_allowlist,tool_allowlist,children,limits,delegation,canonical_payload,source_path
			) values($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,$12::jsonb,$13::jsonb,$14::jsonb,$15::jsonb,$16::jsonb,$17::jsonb,$18)
			on conflict(definition_identity,definition_version) do nothing
		`, definition.Identity, definition.Version, definition.SHA256, definition.Executor,
			definition.ModelPolicy.Identity, definition.ModelPolicy.Version, string(prompts),
			definition.Contracts.Input.Identity, definition.Contracts.Input.Version,
			definition.Contracts.Result.Identity, definition.Contracts.Result.Version,
			string(skills), string(tools), string(children), string(limits), delegation, string(payload), definition.SourcePath); err != nil {
			return fmt.Errorf("register definition %s: %w", definition.Reference(), err)
		}
		if err := verifyCatalogEntry(ctx, tx, "agent_definition_versions", "definition_identity", "definition_version", definition.Reference(), definition.SHA256, payload, definition.SourcePath, "definition"); err != nil {
			return err
		}
	}
	for _, manifest := range catalog.Releases() {
		payload, err := json.Marshal(manifest)
		if err != nil {
			return err
		}
		roots, _ := json.Marshal(manifest.Roots)
		if _, err := tx.Exec(ctx, `
			insert into agent_release_manifests(
				release_identity,release_version,canonical_sha256,roots,canonical_payload,source_path
			) values($1,$2,$3,$4::jsonb,$5::jsonb,$6)
			on conflict(release_identity,release_version) do nothing
		`, manifest.Identity, manifest.Version, manifest.SHA256, string(roots), string(payload), manifest.SourcePath); err != nil {
			return fmt.Errorf("register release %s: %w", manifest.Reference(), err)
		}
		if err := verifyCatalogEntry(ctx, tx, "agent_release_manifests", "release_identity", "release_version", manifest.Reference(), manifest.SHA256, payload, manifest.SourcePath, "release"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func VerifyAgentCatalogReady(ctx context.Context, db *DB, catalog agentcatalog.Catalog, release agentcatalog.Reference) (agentcatalog.ReleaseManifest, error) {
	if db == nil || db.pool == nil {
		return agentcatalog.ReleaseManifest{}, errors.New("nil database")
	}
	if release.Identity == "" {
		return agentcatalog.ReleaseManifest{}, errors.New("Agent release is required")
	}
	manifest, ok := catalog.ResolveRelease(release)
	if !ok {
		return agentcatalog.ReleaseManifest{}, fmt.Errorf("unsupported exact Agent release %s", release)
	}
	manifestPayload, _ := json.Marshal(manifest)
	if err := verifyCatalogEntry(ctx, db.pool, "agent_release_manifests", "release_identity", "release_version", manifest.Reference(), manifest.SHA256, manifestPayload, manifest.SourcePath, "release"); err != nil {
		return agentcatalog.ReleaseManifest{}, err
	}
	for _, contract := range catalog.Contracts() {
		payload, _ := json.Marshal(contract)
		if err := verifyCatalogEntry(ctx, db.pool, "agent_contract_versions", "contract_identity", "contract_version", contract.Reference(), contract.SHA256, payload, contract.SourcePath, "contract"); err != nil {
			return agentcatalog.ReleaseManifest{}, err
		}
	}
	for _, policy := range catalog.ModelPolicies() {
		payload, _ := json.Marshal(policy)
		if err := verifyCatalogEntry(ctx, db.pool, "agent_model_policy_versions", "policy_identity", "policy_version", policy.Reference(), policy.SHA256, payload, policy.SourcePath, "model policy"); err != nil {
			return agentcatalog.ReleaseManifest{}, err
		}
	}
	for _, capability := range catalog.ProviderCapabilities() {
		payload, _ := json.Marshal(capability)
		if err := verifyCatalogEntry(ctx, db.pool, "provider_model_capability_versions", "capability_identity", "capability_version", capability.Reference(), capability.SHA256, payload, capability.SourcePath, "provider capability"); err != nil {
			return agentcatalog.ReleaseManifest{}, err
		}
	}
	for _, policy := range catalog.ModelContextPolicies() {
		payload, _ := json.Marshal(policy)
		if err := verifyCatalogEntry(ctx, db.pool, "agent_model_context_policy_versions", "context_policy_identity", "context_policy_version", policy.Reference(), policy.SHA256, payload, policy.SourcePath, "model context policy"); err != nil {
			return agentcatalog.ReleaseManifest{}, err
		}
	}
	for _, definition := range catalog.Definitions() {
		payload, _ := json.Marshal(definition)
		if err := verifyCatalogEntry(ctx, db.pool, "agent_definition_versions", "definition_identity", "definition_version", definition.Reference(), definition.SHA256, payload, definition.SourcePath, "definition"); err != nil {
			return agentcatalog.ReleaseManifest{}, err
		}
	}
	return manifest, nil
}

type catalogEntryQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func verifyCatalogEntry(ctx context.Context, query catalogEntryQuerier, table, identityColumn, versionColumn string, reference agentcatalog.Reference, sha256 string, payload []byte, sourcePath, kind string) error {
	statement := fmt.Sprintf("select canonical_sha256,canonical_payload,source_path from %s where %s=$1 and %s=$2", table, identityColumn, versionColumn)
	var stored registeredCatalogEntry
	if err := query.QueryRow(ctx, statement, reference.Identity, reference.Version).Scan(&stored.SHA256, &stored.Payload, &stored.SourcePath); err != nil {
		return fmt.Errorf("load registered %s %s: %w", kind, reference, err)
	}
	if stored.SHA256 != sha256 || stored.SourcePath != sourcePath || !sameJSON(stored.Payload, payload) {
		return fmt.Errorf("immutable %s conflict for %s", kind, reference)
	}
	return nil
}
