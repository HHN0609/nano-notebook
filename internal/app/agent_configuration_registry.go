package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/huangxinxinyu/nano-notebook/internal/agent"
	"github.com/jackc/pgx/v5"
)

func RegisterAgentConfiguration(ctx context.Context, db *DB, promptSet agent.AgentPromptSet, configuration agent.AgentConfigurationSet) error {
	if db == nil || db.pool == nil || configuration.PromptSetID != promptSet.ID || configuration.PromptSetHash != promptSet.SHA256 {
		return errors.New("invalid Agent Configuration registration")
	}
	bindings, err := json.Marshal(promptSet.Bindings)
	if err != nil {
		return err
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		insert into agent_prompt_sets(id,canonical_sha256,bindings) values($1,$2,$3::jsonb)
		on conflict(id) do nothing
	`, promptSet.ID, promptSet.SHA256, string(bindings)); err != nil {
		return err
	}
	var storedPromptHash string
	var storedBindings []byte
	if err := tx.QueryRow(ctx, `select canonical_sha256,bindings from agent_prompt_sets where id=$1`, promptSet.ID).Scan(&storedPromptHash, &storedBindings); err != nil {
		return err
	}
	if storedPromptHash != promptSet.SHA256 || !sameJSON(storedBindings, bindings) {
		return fmt.Errorf("immutable Agent Prompt Set conflict for %s", promptSet.ID)
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_configuration_sets(id,canonical_sha256,prompt_set_id,prompt_set_sha256)
		values($1,$2,$3,$4) on conflict(id) do nothing
	`, configuration.ID, configuration.SHA256, configuration.PromptSetID, configuration.PromptSetHash); err != nil {
		return err
	}
	var storedConfigHash, storedPromptSetID, storedPromptSetHash string
	if err := tx.QueryRow(ctx, `
		select canonical_sha256,prompt_set_id,prompt_set_sha256 from agent_configuration_sets where id=$1
	`, configuration.ID).Scan(&storedConfigHash, &storedPromptSetID, &storedPromptSetHash); err != nil {
		return err
	}
	if storedConfigHash != configuration.SHA256 || storedPromptSetID != configuration.PromptSetID || storedPromptSetHash != configuration.PromptSetHash {
		return fmt.Errorf("immutable Agent Configuration Set conflict for %s", configuration.ID)
	}
	for _, role := range []agent.AgentRole{agent.RoleLeader, agent.RoleResearch} {
		profile := configuration.Profiles[role]
		promptPurposes, _ := json.Marshal(profile.PromptPurposes)
		tools, _ := json.Marshal(profile.ToolAllowlist)
		runConfig, _ := json.Marshal(profile.Run)
		profileHash, err := agent.RoleProfileSHA256(profile)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			insert into agent_role_profiles(
				configuration_set_id,role,canonical_sha256,executor_version,model,prompt_purposes,tool_allowlist,run_config,max_attempts
			) values($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb,$8::jsonb,$9)
			on conflict(configuration_set_id,role) do nothing
		`, configuration.ID, role, profileHash, profile.ExecutorVersion, profile.Model, string(promptPurposes), string(tools), string(runConfig), profile.MaxAttempts); err != nil {
			return err
		}
		var storedHash string
		if err := tx.QueryRow(ctx, `
			select canonical_sha256 from agent_role_profiles where configuration_set_id=$1 and role=$2
		`, configuration.ID, role).Scan(&storedHash); err != nil {
			return err
		}
		if storedHash != profileHash {
			return fmt.Errorf("immutable Agent Role Profile conflict for %s/%s", configuration.ID, role)
		}
	}
	return tx.Commit(ctx)
}

func VerifyAgentConfigurationReady(ctx context.Context, db *DB, supported agent.AgentConfigurationSet) error {
	if db == nil || db.pool == nil {
		return errors.New("nil database")
	}
	var registeredHash string
	if err := db.pool.QueryRow(ctx, `select canonical_sha256 from agent_configuration_sets where id=$1`, supported.ID).Scan(&registeredHash); err != nil {
		return fmt.Errorf("load supported Agent Configuration Set: %w", err)
	}
	if registeredHash != supported.SHA256 {
		return fmt.Errorf("unsupported Agent Configuration Set %s", supported.ID)
	}
	var unsupported string
	err := db.pool.QueryRow(ctx, `
		select r.agent_config_id
		from agent_runs r
		where r.status in ('queued','running') and r.agent_config_id<>$1
		order by r.created_at,r.id limit 1
	`, supported.ID).Scan(&unsupported)
	if err == nil {
		return fmt.Errorf("non-terminal Run references unsupported Agent Configuration Set %s", unsupported)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}
