package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/huangxinxinyu/nano-notebook/internal/realtime"
	"github.com/huangxinxinyu/nano-notebook/internal/websearch"
	"github.com/jackc/pgx/v5"
)

const (
	ResearchStepQueryPlan    = "query_plan"
	ResearchStepSearchResult = "search_result"
)

var ErrResearchAuthorityLost = errors.New("Research authority lost")

type ResearchQueryPlan struct {
	Queries []string `json:"queries"`
}

type ResearchSearchResult struct {
	Query      string                `json:"query"`
	Candidates []websearch.Candidate `json:"candidates"`
}

type RoleCheckpoint struct {
	Role          AgentRole
	Step          string
	Ordinal       int
	IdentityKey   string
	Payload       json.RawMessage
	PayloadSHA256 string
}

type ResearchProgress struct {
	Plan    []string
	Results map[int][]websearch.Candidate
}

func (p ResearchProgress) FirstMissing() (int, string, bool) {
	for ordinal, query := range p.Plan {
		if _, accepted := p.Results[ordinal]; !accepted {
			return ordinal, query, true
		}
	}
	return 0, "", false
}

func NewRoleCheckpoint(role AgentRole, step string, ordinal int, payload any) (RoleCheckpoint, error) {
	step = strings.TrimSpace(step)
	if role != RoleResearch || (step != ResearchStepQueryPlan && step != ResearchStepSearchResult) ||
		(step == ResearchStepQueryPlan && ordinal != 0) || (step == ResearchStepSearchResult && (ordinal < 0 || ordinal > 2)) {
		return RoleCheckpoint{}, errors.New("invalid Role Checkpoint identity")
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) == 0 || len(encoded) > 256*1024 || encoded[0] != '{' {
		return RoleCheckpoint{}, errors.New("invalid Role Checkpoint payload")
	}
	digest := sha256.Sum256(encoded)
	return RoleCheckpoint{
		Role: role, Step: step, Ordinal: ordinal,
		IdentityKey: fmt.Sprintf("%s/%s/%d", role, step, ordinal),
		Payload:     encoded, PayloadSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func AppendRoleCheckpointInTx(ctx context.Context, tx pgx.Tx, attempt Attempt, checkpoint RoleCheckpoint) error {
	if tx == nil || checkpoint.IdentityKey == "" || len(checkpoint.PayloadSHA256) != 64 {
		return ErrCheckpointInvalid
	}
	if err := requireResearchCheckpointAuthority(ctx, tx, attempt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		insert into agent_role_checkpoints(run_id,agent_role,step_key,ordinal,identity_key,payload,payload_sha256)
		select r.id,$5,$6,$7,$8,$9::jsonb,$10
		from agent_runs r join agent_jobs j on j.run_id=r.id
		join chat_chats chat on chat.id=r.chat_id
		join notebook_memberships member on member.notebook_id=chat.notebook_id and member.user_id=r.user_id and member.role in ('owner','editor')
		where r.id=$1 and r.agent_role=$5 and r.status='running' and r.deadline_at>now()
		  and j.id=$2 and j.status='running' and j.attempt_no=$3 and j.lease_token=$4::uuid and j.lease_expires_at>now()
		on conflict(run_id,agent_role,step_key,ordinal) do nothing
	`, attempt.RunID, attempt.JobID, attempt.AttemptNo, attempt.LeaseToken, checkpoint.Role, checkpoint.Step,
		checkpoint.Ordinal, checkpoint.IdentityKey, string(checkpoint.Payload), checkpoint.PayloadSHA256); err != nil {
		return err
	}
	var storedHash string
	if err := tx.QueryRow(ctx, `
		select payload_sha256 from agent_role_checkpoints
		where run_id=$1 and agent_role=$2 and step_key=$3 and ordinal=$4
	`, attempt.RunID, checkpoint.Role, checkpoint.Step, checkpoint.Ordinal).Scan(&storedHash); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if authorityErr := requireResearchCheckpointAuthority(ctx, tx, attempt); authorityErr != nil {
				return authorityErr
			}
			return ErrLeaseLost
		}
		return err
	}
	if storedHash != checkpoint.PayloadSHA256 {
		return ErrCheckpointInvalid
	}
	return RecordRoleCheckpointAcceptedInTx(ctx, tx, attempt, checkpoint)
}

func LoadResearchProgressInTx(ctx context.Context, tx pgx.Tx, attempt Attempt) (ResearchProgress, error) {
	if tx == nil {
		return ResearchProgress{}, ErrCheckpointInvalid
	}
	if err := requireResearchCheckpointAuthority(ctx, tx, attempt); err != nil {
		return ResearchProgress{}, err
	}
	rows, err := tx.Query(ctx, `
		select step_key,ordinal,payload from agent_role_checkpoints
		where run_id=$1 and agent_role='research'
		order by case step_key when 'query_plan' then 0 else 1 end,ordinal
	`, attempt.RunID)
	if err != nil {
		return ResearchProgress{}, err
	}
	defer rows.Close()
	progress := ResearchProgress{Results: make(map[int][]websearch.Candidate, 3)}
	for rows.Next() {
		var step string
		var ordinal int
		var payload []byte
		if err := rows.Scan(&step, &ordinal, &payload); err != nil {
			return ResearchProgress{}, err
		}
		switch step {
		case ResearchStepQueryPlan:
			var plan ResearchQueryPlan
			if len(progress.Plan) != 0 || json.Unmarshal(payload, &plan) != nil || len(boundQueries(plan.Queries)) != len(plan.Queries) || len(plan.Queries) < 1 {
				return ResearchProgress{}, ErrCheckpointInvalid
			}
			progress.Plan = append([]string(nil), plan.Queries...)
		case ResearchStepSearchResult:
			var result ResearchSearchResult
			if _, duplicate := progress.Results[ordinal]; duplicate || json.Unmarshal(payload, &result) != nil || ordinal < 0 || ordinal >= len(progress.Plan) ||
				strings.TrimSpace(result.Query) == "" || result.Query != progress.Plan[ordinal] || len(result.Candidates) > 10 {
				return ResearchProgress{}, ErrCheckpointInvalid
			}
			progress.Results[ordinal] = append([]websearch.Candidate(nil), result.Candidates...)
		default:
			return ResearchProgress{}, ErrCheckpointInvalid
		}
	}
	if err := rows.Err(); err != nil {
		return ResearchProgress{}, err
	}
	for ordinal := range progress.Results {
		if len(progress.Plan) == 0 || ordinal >= len(progress.Plan) {
			return ResearchProgress{}, ErrCheckpointInvalid
		}
	}
	return progress, nil
}

func requireResearchCheckpointAuthority(ctx context.Context, tx pgx.Tx, attempt Attempt) error {
	var valid, authorized bool
	if err := tx.QueryRow(ctx, `
		select exists(select 1 from agent_runs r join agent_jobs j on j.run_id=r.id
		where r.id=$1 and r.agent_role='research' and r.status='running' and r.deadline_at>now()
		  and j.id=$2 and j.status='running' and j.attempt_no=$3 and j.lease_token=$4::uuid and j.lease_expires_at>now()),
		exists(select 1 from agent_runs r join chat_chats chat on chat.id=r.chat_id
		  join notebook_memberships member on member.notebook_id=chat.notebook_id and member.user_id=r.user_id
		  where r.id=$1 and member.role in ('owner','editor'))
	`, attempt.RunID, attempt.JobID, attempt.AttemptNo, attempt.LeaseToken).Scan(&valid, &authorized); err != nil {
		return err
	}
	if !valid {
		return ErrLeaseLost
	}
	if !authorized {
		return ErrResearchAuthorityLost
	}
	return nil
}

// FailResearchPayloadInTx terminalizes Research-owned product state while the
// Delegation Kernel remains the sole owner of generic Run/Job/relationship state.
func FailResearchPayloadInTx(ctx context.Context, tx pgx.Tx, runID, errorCode string) error {
	if tx == nil || strings.TrimSpace(runID) == "" || !safeAttemptErrorCode.MatchString(errorCode) {
		return errors.New("invalid Research payload failure")
	}
	rows, err := tx.Query(ctx, `
		update source_discovery_sessions set status='failed',error_code=$2,completed_at=now(),updated_at=now()
		where research_run_id=$1 and origin='research_agent' and status='searching'
		returning id
	`, runID, errorCode)
	if err != nil {
		return err
	}
	sessionIDs := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			rows.Close()
			return err
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, sessionID := range sessionIDs {
		if err := realtime.NotifySourceDiscovery(ctx, tx, sessionID); err != nil {
			return err
		}
	}
	return nil
}
