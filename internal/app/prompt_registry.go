package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
	"github.com/jackc/pgx/v5"
)

type registeredPromptVersion struct {
	Contract string
	Content  string
	SHA256   string
}

func registerEmbeddedPromptCatalog(ctx context.Context, db *DB) error {
	catalog, err := promptcatalog.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load embedded Prompt Catalog: %w", err)
	}
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, definition := range catalog.Versions() {
		if _, err := tx.Exec(ctx, `
			insert into agent_prompt_versions(
				prompt_identity,prompt_version,output_contract,canonical_sha256,content,source_path
			) values($1,$2,$3,$4,$5,$6)
			on conflict(prompt_identity,prompt_version) do nothing
		`, definition.Identity, definition.Version, definition.Contract, definition.SHA256, definition.Content, definition.SourcePath); err != nil {
			return fmt.Errorf("register prompt %s@%d: %w", definition.Identity, definition.Version, err)
		}
		var stored registeredPromptVersion
		if err := tx.QueryRow(ctx, `
			select output_contract,content,canonical_sha256
			from agent_prompt_versions
			where prompt_identity=$1 and prompt_version=$2
		`, definition.Identity, definition.Version).Scan(&stored.Contract, &stored.Content, &stored.SHA256); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("registered prompt %s@%d disappeared", definition.Identity, definition.Version)
			}
			return err
		}
		if err := validateRegisteredPrompt(definition, stored); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// VerifyEmbeddedPromptCatalog is the Worker readiness gate. It is read-only so
// execution hosts never need Prompt registration authority.
func VerifyEmbeddedPromptCatalog(ctx context.Context, db *DB) error {
	if db == nil || db.pool == nil {
		return errors.New("nil database")
	}
	catalog, err := promptcatalog.LoadEmbedded()
	if err != nil {
		return fmt.Errorf("load embedded Prompt Catalog: %w", err)
	}
	for _, definition := range catalog.Versions() {
		var stored registeredPromptVersion
		if err := db.pool.QueryRow(ctx, `
			select output_contract,content,canonical_sha256
			from agent_prompt_versions
			where prompt_identity=$1 and prompt_version=$2
		`, definition.Identity, definition.Version).Scan(&stored.Contract, &stored.Content, &stored.SHA256); err != nil {
			return fmt.Errorf("load registered prompt %s@%d: %w", definition.Identity, definition.Version, err)
		}
		if err := validateRegisteredPrompt(definition, stored); err != nil {
			return err
		}
	}
	return nil
}

func validateRegisteredPrompt(definition promptcatalog.PromptVersion, stored registeredPromptVersion) error {
	if stored.Contract != definition.Contract || stored.Content != definition.Content || stored.SHA256 != definition.SHA256 {
		return fmt.Errorf("immutable prompt conflict for %s@%d", definition.Identity, definition.Version)
	}
	return nil
}
