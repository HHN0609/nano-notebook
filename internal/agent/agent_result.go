package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/jackc/pgx/v5"
)

const maxAgentResultBytes = 1024 * 1024

type AgentResult struct {
	ID             string
	ProducerRunID  string
	Contract       agentcatalog.Reference
	ContractSHA256 string
	Payload        json.RawMessage
	PayloadSHA256  string
	PayloadBytes   int
}

func NewAgentResult(id, producerRunID string, contract agentcatalog.ContractVersion, payload json.RawMessage) (AgentResult, error) {
	id = strings.TrimSpace(id)
	producerRunID = strings.TrimSpace(producerRunID)
	if id == "" || producerRunID == "" || len(contract.SHA256) != 64 {
		return AgentResult{}, errors.New("invalid Agent Result identity or Contract")
	}
	canonical, err := CanonicalJSONObject(payload)
	if err != nil || len(canonical) > maxAgentResultBytes {
		return AgentResult{}, errors.New("invalid or oversized Agent Result payload")
	}
	digest := sha256.Sum256(canonical)
	return AgentResult{
		ID: id, ProducerRunID: producerRunID, Contract: contract.Reference(), ContractSHA256: contract.SHA256,
		Payload: canonical, PayloadSHA256: hex.EncodeToString(digest[:]), PayloadBytes: len(canonical),
	}, nil
}

func StoreAgentResultInTx(ctx context.Context, tx pgx.Tx, result AgentResult) error {
	if tx == nil || result.ID == "" || result.ProducerRunID == "" || result.Contract.Identity == "" || result.Contract.Version < 1 ||
		len(result.ContractSHA256) != 64 || len(result.PayloadSHA256) != 64 || result.PayloadBytes != len(result.Payload) || result.PayloadBytes < 1 {
		return errors.New("invalid Agent Result")
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_run_results(
			id,producer_run_id,contract_identity,contract_version,contract_sha256,payload,payload_sha256,payload_bytes
		) values($1,$2,$3,$4,$5,$6::jsonb,$7,$8)
		on conflict(producer_run_id) do nothing
	`, result.ID, result.ProducerRunID, result.Contract.Identity, result.Contract.Version, result.ContractSHA256,
		string(result.Payload), result.PayloadSHA256, result.PayloadBytes); err != nil {
		return err
	}
	var stored AgentResult
	var storedPayload []byte
	if err := tx.QueryRow(ctx, `
		select id,producer_run_id,contract_identity,contract_version,contract_sha256,payload,payload_sha256,payload_bytes
		from agent_run_results where producer_run_id=$1
	`, result.ProducerRunID).Scan(
		&stored.ID, &stored.ProducerRunID, &stored.Contract.Identity, &stored.Contract.Version, &stored.ContractSHA256,
		&storedPayload, &stored.PayloadSHA256, &stored.PayloadBytes,
	); err != nil {
		return err
	}
	stored.Payload, _ = CanonicalJSONObject(json.RawMessage(storedPayload))
	if stored.ID != result.ID || stored.ProducerRunID != result.ProducerRunID || stored.Contract != result.Contract ||
		stored.ContractSHA256 != result.ContractSHA256 || stored.PayloadSHA256 != result.PayloadSHA256 ||
		stored.PayloadBytes != result.PayloadBytes || !reflect.DeepEqual(stored.Payload, result.Payload) {
		return fmt.Errorf("immutable Agent Result conflict for producer %s", result.ProducerRunID)
	}
	return nil
}
