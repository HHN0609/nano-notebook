package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/huangxinxinyu/nano-notebook/internal/agentobs"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	researchCapsuleMarkdownLimit = 24_000
	researchRollupMarkdownLimit  = 64_000
	researchMemoryMarkdownLimit  = 48_000
	researchExactRecentTokens    = 12_000
	researchRollupStepInterval   = 8
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]+\]\((https?://[^\s)]+)\)`)
var chineseReadCountClaimPattern = regexp.MustCompile(`其中\s*\*{0,2}\d+\s*个为成功读取的一手材料\*{0,2}(?:（[^）\n]*）)?`)
var englishReadCountClaimPattern = regexp.MustCompile(`(?i)\b(?:of which\s+)?\*{0,2}\d+\*{0,2}\s+(?:were\s+)?successfully read(?:\s+(?:primary\s+)?sources?)?\b`)

type ResearchRuntime struct {
	base    *PostgresRuntime
	pool    *pgxpool.Pool
	prompts promptcatalog.Catalog
}

func NewResearchRuntime(pool *pgxpool.Pool, prompts promptcatalog.Catalog) (*ResearchRuntime, error) {
	if pool == nil {
		return nil, errors.New("Research Runtime requires PostgreSQL")
	}
	for _, identity := range []string{"agent.deep-research-executor", "agent.deep-research-reporter"} {
		if _, ok := prompts.Resolve(identity, 1); !ok {
			return nil, fmt.Errorf("Research Prompt %s@1 is missing", identity)
		}
	}
	return &ResearchRuntime{base: NewPostgresRuntime(pool, "", nil), pool: pool, prompts: prompts}, nil
}

func (r *ResearchRuntime) Load(ctx context.Context, attempt Attempt) (Execution, error) {
	execution, err := r.base.Load(ctx, attempt)
	if err != nil {
		return Execution{}, err
	}
	tx, err := r.base.workerTx(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `update research_sessions set status='running',updated_at=now() where execution_run_id=$1 and status='queued'`, attempt.RunID); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return execution, nil
}

func (r *ResearchRuntime) LoadCheckpointPrefix(ctx context.Context, attempt Attempt) (CheckpointPrefix, error) {
	return r.base.LoadCheckpointPrefix(ctx, attempt)
}

func (r *ResearchRuntime) CheckAuthority(ctx context.Context, attempt Attempt) error {
	return r.base.CheckAuthority(ctx, attempt)
}

func (r *ResearchRuntime) AppendCheckpoint(ctx context.Context, attempt Attempt, checkpoint PendingCheckpoint) (Checkpoint, error) {
	stored, err := r.base.AppendCheckpoint(ctx, attempt, checkpoint)
	if err != nil {
		return Checkpoint{}, err
	}
	if checkpoint.Kind == CheckpointActionResult {
		if err := r.materializeCompletedStep(ctx, attempt, checkpoint.DecisionNo); err != nil {
			return Checkpoint{}, err
		}
	}
	return stored, nil
}

func (r *ResearchRuntime) BuildDecisionRequest(ctx context.Context, execution Execution, prefix CheckpointPrefix, definitions []models.ActionDefinition) (models.ModelRequest, error) {
	if prefix.Final != nil {
		return models.ModelRequest{}, errors.New("Research Final Draft does not require another decision")
	}
	var sessionID, originalRequest, planJSON, executorReference, reporterReference string
	err := r.pool.QueryRow(ctx, `
		select session.id,message.content,plan.plan_json::text,
			definition.prompt_bindings->>'executor',definition.prompt_bindings->>'reporter'
		from research_sessions session
		join chat_messages message on message.id=session.input_message_id
		join research_plan_versions plan on plan.session_id=session.id and plan.version=session.accepted_plan_version
		join agent_runs run on run.id=session.execution_run_id
		join agent_definition_versions definition on definition.definition_identity=run.definition_identity and definition.definition_version=run.definition_version
		where session.execution_run_id=$1 and session.status in ('running','publishing')
	`, execution.RunID).Scan(&sessionID, &originalRequest, &planJSON, &executorReference, &reporterReference)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ModelRequest{}, ErrLeaseLost
	}
	if err != nil {
		return models.ModelRequest{}, err
	}
	executorPrompt, err := r.resolvePrompt(executorReference)
	if err != nil {
		return models.ModelRequest{}, err
	}
	reporterPrompt, err := r.resolvePrompt(reporterReference)
	if err != nil {
		return models.ModelRequest{}, err
	}
	system := strings.TrimSpace(executorPrompt.Content) + "\n\nFinal report requirements:\n" + strings.TrimSpace(reporterPrompt.Content)
	system += "\n\n" + researchExecutionControlPrompt(execution.ActionBatchLimit)
	forceReport := consecutiveResearchDuplicateSteps(prefix) >= 3
	if forceReport {
		system += "\n\nRecovery directive: the last tool decisions repeated already completed inputs. Tool use is now closed for this Run. Produce the complete final report immediately from the successfully read Evidence Ledger, explicitly noting remaining gaps."
		definitions = nil
	}
	messages := []models.ModelMessage{
		{Role: models.RoleSystem, Content: system},
		{Role: models.RoleUser, Content: "Original request:\n" + originalRequest + "\n\nAccepted Research Plan (immutable):\n" + planJSON},
	}
	memory, err := r.loadResearchMemory(ctx, sessionID)
	if err != nil {
		return models.ModelRequest{}, err
	}
	if memory != "" {
		messages = append(messages, models.ModelMessage{Role: models.RoleAssistant, Content: memory})
	}
	projected, err := ProjectChatLane(ctx, ChatLane{Turns: []ChatLaneTurn{{
		MessageID: execution.InputMessageID, Content: originalRequest,
		Runs: []ChatLaneRun{{RunID: execution.RunID, Prefix: &prefix}},
	}}}, nil)
	if err != nil {
		return models.ModelRequest{}, err
	}
	keepRecent := execution.ModelContext.Policy.KeepRecentTokens
	if keepRecent > researchExactRecentTokens {
		keepRecent = researchExactRecentTokens
	}
	exact := selectRecentResearchUnits(projected[1:], keepRecent)
	messages = append(messages, FlattenContextUnits(exact)...)
	return models.ModelRequest{
		Model: execution.Model, Messages: messages, ActionDefinitions: cloneActionDefinitions(definitions),
		InvocationPolicy: execution.ModelInvocation,
	}, nil
}

func researchExecutionControlPrompt(actionBatchLimit int) string {
	return fmt.Sprintf("Tool-call contract: propose at most %d tool calls in one model decision. Each `web_search` call contains one to three queries. Each `read_url` call contains exactly one URL. Split larger reading sets across multiple decisions; never emit an oversized batch.", actionBatchLimit)
}

func (r *ResearchRuntime) PrepareFinal(ctx context.Context, _ Attempt, execution Execution, _ CheckpointPrefix, draft models.FinalDraft) (models.FinalDraft, error) {
	if err := draft.Validate(); err != nil {
		return models.FinalDraft{}, err
	}
	var total, discoveredOnly, read, failed int
	if err := r.pool.QueryRow(ctx, `
		select count(*),
			count(*) filter(where ledger.status='discovered'),
			count(*) filter(where ledger.status='read'),
			count(*) filter(where ledger.status='failed')
		from research_evidence_ledger ledger
		join research_sessions session on session.id=ledger.session_id
		where session.execution_run_id=$1
	`, execution.RunID).Scan(&total, &discoveredOnly, &read, &failed); err != nil {
		return models.FinalDraft{}, err
	}
	draft.Text, _ = canonicalizeResearchEvidenceClaims(draft.Text, total, discoveredOnly, read, failed)
	return draft, nil
}

func consecutiveResearchDuplicateSteps(prefix CheckpointPrefix) int {
	count := 0
	for index := len(prefix.Proposals) - 1; index >= 0; index-- {
		proposal := prefix.Proposals[index]
		if len(proposal.Actions) == 0 {
			break
		}
		duplicateStep := true
		for _, action := range proposal.Actions {
			if action.Result == nil || action.Result.Status != ActionDomainError || action.Result.ErrorCode != "research_duplicate_action" {
				duplicateStep = false
				break
			}
		}
		if !duplicateStep {
			break
		}
		count++
	}
	return count
}

func (r *ResearchRuntime) PublishFinal(ctx context.Context, attempt Attempt, draft models.FinalDraft) error {
	if err := draft.Validate(); err != nil {
		return err
	}
	traceCtx, traceScope, err := r.base.beginTraceScope(ctx)
	if err != nil {
		return err
	}
	defer traceScope.Rollback()
	tx, err := r.base.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockCheckpointAuthority(ctx, tx, attempt); err != nil {
		return err
	}
	checkpoints, err := loadRunCheckpoints(ctx, tx, attempt.RunID)
	if err != nil {
		return err
	}
	prefix, err := LoadCheckpointPrefix(ctx, checkpoints)
	if err != nil {
		return err
	}
	storedHash, storedErr := finalDraftSHA256(valueOrEmptyFinal(prefix.Final))
	expectedHash, expectedErr := finalDraftSHA256(draft)
	if prefix.Final == nil || storedErr != nil || expectedErr != nil || storedHash != expectedHash {
		return invalidCheckpoint("Research report publication does not match accepted Final Draft")
	}
	var sessionID, chatID string
	if err := tx.QueryRow(ctx, `select id,chat_id from research_sessions where execution_run_id=$1 and status='running' for update`, attempt.RunID).Scan(&sessionID, &chatID); err != nil {
		return err
	}
	reportText, removedLinks, err := sanitizeResearchReportLinks(ctx, tx, sessionID, draft.Text)
	if err != nil {
		return err
	}
	if len(removedLinks) > 0 {
		reportText += fmt.Sprintf("\n\n> Publication note: %d discovered-only link(s) were downgraded to plain text because their pages were not successfully read.\n", len(removedLinks))
	}
	if err := storeConfiguredFinalResult(ctx, tx, attempt.RunID, reportText); err != nil {
		return err
	}
	var reportVersion int
	if err := tx.QueryRow(ctx, `select coalesce(max(version),0)+1 from research_report_versions where session_id=$1`, sessionID).Scan(&reportVersion); err != nil {
		return err
	}
	var discovered, read, failed int
	if err := tx.QueryRow(ctx, `select count(*),count(*) filter(where status='read'),count(*) filter(where status='failed') from research_evidence_ledger where session_id=$1`, sessionID).Scan(&discovered, &read, &failed); err != nil {
		return err
	}
	stats, _ := json.Marshal(map[string]int{"discovered": discovered, "read": read, "failed": failed, "downgraded_unread_links": len(removedLinks)})
	if _, err := tx.Exec(ctx, `insert into research_report_versions(session_id,version,producer_run_id,content_markdown,evidence_stats) values($1,$2,$3,$4,$5::jsonb)`, sessionID, reportVersion, attempt.RunID, reportText, string(stats)); err != nil {
		return err
	}
	messageID := "msg_" + uuid.NewString()
	if _, err := tx.Exec(ctx, `insert into chat_messages(id,chat_id,role,content) values($1,$2,'assistant',$3)`, messageID, chatID, reportText); err != nil {
		return err
	}
	if tag, err := tx.Exec(ctx, `update agent_runs set status='completed',output_message_id=$2,finished_at=now(),updated_at=now() where id=$1 and status='running' and output_message_id is null`, attempt.RunID, messageID); err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return ErrLeaseLost
	}
	if tag, err := tx.Exec(ctx, `update agent_jobs set status='succeeded',lease_token=null,lease_expires_at=null,finished_at=now(),updated_at=now() where id=$1 and run_id=$2 and status='running' and lease_token=$3::uuid`, attempt.JobID, attempt.RunID, attempt.LeaseToken); err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return ErrLeaseLost
	}
	if tag, err := tx.Exec(ctx, `update research_sessions set status='completed',current_report_version=$2,completed_at=now(),updated_at=now() where id=$1 and status='running'`, sessionID, reportVersion); err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return err
		}
		return ErrLeaseLost
	}
	if err := RecordRunTerminalInTx(traceCtx, tx, attempt.RunID, RunTerminalTrace{RunStatus: "completed", SpanStatus: agentobs.StatusOK, AttemptNo: attempt.AttemptNo}); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select pg_notify('nano_agent_runs',$1)`, attempt.RunID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	publishCommittedTrace(traceCtx, traceScope)
	return nil
}

func (r *ResearchRuntime) Fail(ctx context.Context, attempt Attempt, errorCode string) error {
	if err := r.base.Fail(ctx, attempt, errorCode); err != nil {
		return err
	}
	tx, err := r.base.workerTx(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if _, err := tx.Exec(context.WithoutCancel(ctx), `update research_sessions set status='failed',error_code=$2,updated_at=now() where execution_run_id=$1 and status in ('queued','running','publishing')`, attempt.RunID, errorCode); err != nil {
		return err
	}
	return tx.Commit(context.WithoutCancel(ctx))
}

func (r *ResearchRuntime) resolvePrompt(reference string) (promptcatalog.PromptVersion, error) {
	identity, version, err := parseVersionedIdentity(reference)
	if err != nil {
		return promptcatalog.PromptVersion{}, err
	}
	prompt, ok := r.prompts.Resolve(identity, version)
	if !ok {
		return promptcatalog.PromptVersion{}, fmt.Errorf("Research Prompt %s is unavailable", reference)
	}
	return prompt, nil
}

func (r *ResearchRuntime) materializeCompletedStep(ctx context.Context, attempt Attempt, decisionNo int) error {
	prefix, err := r.base.LoadCheckpointPrefix(ctx, attempt)
	if err != nil {
		return err
	}
	var proposal *AcceptedProposal
	for index := range prefix.Proposals {
		if prefix.Proposals[index].DecisionNo == decisionNo {
			proposal = &prefix.Proposals[index]
			break
		}
	}
	if proposal == nil || firstMissingResult(*proposal) >= 0 {
		return nil
	}
	content := buildResearchStepCapsule(*proposal)
	rawHash := sha256.New()
	for _, action := range proposal.Actions {
		rawHash.Write([]byte(action.Name))
		rawHash.Write(action.Input)
		if action.Result != nil {
			rawHash.Write(action.Result.Output)
			rawHash.Write([]byte(action.Result.ErrorCode))
		}
	}
	sourceHash := hex.EncodeToString(rawHash.Sum(nil))
	tx, err := r.base.workerTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var sessionID string
	if err := tx.QueryRow(ctx, `select id from research_sessions where execution_run_id=$1 and status='running'`, attempt.RunID).Scan(&sessionID); err != nil {
		return err
	}
	var startSeq, endSeq int
	if err := tx.QueryRow(ctx, `select min(sequence_no),max(sequence_no) from agent_run_checkpoints where run_id=$1 and decision_no=$2`, attempt.RunID, decisionNo).Scan(&startSeq, &endSeq); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into research_step_capsules(session_id,run_id,decision_no,start_checkpoint_seq,end_checkpoint_seq,content_markdown,source_checkpoint_sha256) values($1,$2,$3,$4,$5,$6,$7) on conflict(run_id,decision_no) do nothing`, sessionID, attempt.RunID, decisionNo, startSeq, endSeq, content, sourceHash); err != nil {
		return err
	}
	for _, action := range proposal.Actions {
		if err := materializeResearchEvidence(ctx, tx, sessionID, attempt.RunID, action); err != nil {
			return err
		}
	}
	if err := maybeCreateResearchRollup(ctx, tx, sessionID, decisionNo); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func buildResearchStepCapsule(proposal AcceptedProposal) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# Agent Step %d\n\n## Intent\nExecute the accepted tool batch and preserve its evidence and routing outcome.\n", proposal.DecisionNo)
	for _, action := range proposal.Actions {
		fmt.Fprintf(&builder, "\n## Tool %s\n\n- Action: `%s`\n- Input: `%s`\n", action.Name, action.ActionID, strings.TrimSpace(string(action.Input)))
		if action.Result == nil {
			builder.WriteString("- Status: incomplete\n")
			continue
		}
		fmt.Fprintf(&builder, "- Status: `%s`\n", action.Result.Status)
		if action.Result.ErrorCode != "" {
			fmt.Fprintf(&builder, "- Error: `%s`\n", action.Result.ErrorCode)
		}
		if action.Result.Status != ActionSucceeded {
			continue
		}
		switch action.Name {
		case "read_url":
			var output readURLOutput
			if json.Unmarshal(action.Result.Output, &output) == nil {
				fmt.Fprintf(&builder, "- Source: [%s](%s)\n- Reader: `%s`; words: %d; truncated: %t\n\n### Retained evidence\n\n%s\n", output.Title, output.FinalURL, output.Engine, output.WordCount, output.Truncated, compactMarkdownByParagraph(output.Markdown, researchCapsuleMarkdownLimit))
				continue
			}
		case "web_search":
			var output webSearchOutput
			if json.Unmarshal(action.Result.Output, &output) == nil {
				for _, result := range output.Results {
					fmt.Fprintf(&builder, "\n### Query: %s\n", result.Query)
					for _, candidate := range result.Candidates {
						fmt.Fprintf(&builder, "- [%s](%s) — %s\n", candidate.Title, candidate.URL, candidate.Description)
					}
				}
				continue
			}
		}
		fmt.Fprintf(&builder, "\n### Result\n\n```json\n%s\n```\n", action.Result.Output)
	}
	return builder.String()
}

func compactMarkdownByParagraph(markdown string, limit int) string {
	markdown = strings.TrimSpace(markdown)
	if len(markdown) <= limit {
		return markdown
	}
	paragraphs := strings.Split(markdown, "\n\n")
	kept := make([]string, 0, len(paragraphs))
	used := 0
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		priority := strings.HasPrefix(paragraph, "#") || strings.Contains(paragraph, "http://") || strings.Contains(paragraph, "https://")
		if used+len(paragraph)+2 <= limit || priority {
			kept = append(kept, paragraph)
			used += len(paragraph) + 2
		}
	}
	return strings.Join(kept, "\n\n") + "\n\n[Paragraph-aware capsule omitted lower-priority body text; raw checkpoint retained.]"
}

func materializeResearchEvidence(ctx context.Context, tx pgx.Tx, sessionID, runID string, action AcceptedAction) error {
	if action.Result == nil {
		return nil
	}
	switch action.Name {
	case "web_search":
		if action.Result.Status != ActionSucceeded {
			return nil
		}
		var output webSearchOutput
		if err := json.Unmarshal(action.Result.Output, &output); err != nil {
			return err
		}
		for _, result := range output.Results {
			for _, candidate := range result.Candidates {
				if _, err := tx.Exec(ctx, `
					insert into research_evidence_ledger(session_id,url,title,status)
					values($1,$2,$3,'discovered')
					on conflict(session_id,url) do update set title=excluded.title,last_seen_at=now(),
						status=case when research_evidence_ledger.status='read' then 'read' else 'discovered' end
				`, sessionID, candidate.URL, candidate.Title); err != nil {
					return err
				}
			}
		}
	case "read_url":
		var input readURLInput
		if err := json.Unmarshal(action.Input, &input); err != nil {
			return err
		}
		if action.Result.Status != ActionSucceeded {
			_, err := tx.Exec(ctx, `insert into research_evidence_ledger(session_id,url,status,read_run_id,read_action_id) values($1,$2,'failed',$3,$4) on conflict(session_id,url) do update set status=case when research_evidence_ledger.status='read' then 'read' else 'failed' end,read_run_id=excluded.read_run_id,read_action_id=excluded.read_action_id,last_seen_at=now()`, sessionID, input.URL, runID, action.ActionID)
			return err
		}
		var output readURLOutput
		if err := json.Unmarshal(action.Result.Output, &output); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(output.Markdown))
		_, err := tx.Exec(ctx, `
			insert into research_evidence_ledger(session_id,url,final_url,title,status,read_run_id,read_action_id,content_sha256,word_count,engine)
			values($1,$2,$3,$4,'read',$5,$6,$7,$8,$9)
			on conflict(session_id,url) do update set final_url=excluded.final_url,title=excluded.title,status='read',read_run_id=excluded.read_run_id,
				read_action_id=excluded.read_action_id,content_sha256=excluded.content_sha256,word_count=excluded.word_count,engine=excluded.engine,last_seen_at=now()
		`, sessionID, input.URL, output.FinalURL, output.Title, runID, action.ActionID, hex.EncodeToString(digest[:]), output.WordCount, output.Engine)
		return err
	}
	return nil
}

func maybeCreateResearchRollup(ctx context.Context, tx pgx.Tx, sessionID string, throughDecision int) error {
	var lastDecision, firstDecision, version int
	var previous, previousHash string
	err := tx.QueryRow(ctx, `select version,first_decision_no,last_decision_no,content_markdown,source_capsules_sha256 from research_rollups where session_id=$1 order by version desc limit 1`, sessionID).Scan(&version, &firstDecision, &lastDecision, &previous, &previousHash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if throughDecision-lastDecision < researchRollupStepInterval {
		return nil
	}
	rows, err := tx.Query(ctx, `select decision_no,content_markdown,source_checkpoint_sha256 from research_step_capsules where session_id=$1 and decision_no>$2 and decision_no<=$3 order by decision_no`, sessionID, lastDecision, throughDecision)
	if err != nil {
		return err
	}
	defer rows.Close()
	var parts []string
	hash := sha256.New()
	first := firstDecision
	if previousHash != "" {
		hash.Write([]byte(previousHash))
	}
	for rows.Next() {
		var decision int
		var content, sourceHash string
		if err := rows.Scan(&decision, &content, &sourceHash); err != nil {
			return err
		}
		if first == 0 {
			first = decision
		}
		parts = append(parts, content)
		hash.Write([]byte(sourceHash))
	}
	if err := rows.Err(); err != nil || first == 0 {
		return err
	}
	rollup := combineResearchRollup(previous, parts)
	_, err = tx.Exec(ctx, `insert into research_rollups(session_id,version,first_decision_no,last_decision_no,content_markdown,source_capsules_sha256) values($1,$2,$3,$4,$5,$6)`, sessionID, version+1, first, throughDecision, rollup, hex.EncodeToString(hash.Sum(nil)))
	return err
}

func combineResearchRollup(previous string, parts []string) string {
	content := strings.TrimSpace(previous)
	if content == "" {
		content = "# Research Rollup"
	}
	if len(parts) > 0 {
		content += "\n\n" + strings.Join(parts, "\n\n")
	}
	return compactMarkdownByParagraph(content, researchRollupMarkdownLimit)
}

func (r *ResearchRuntime) loadResearchMemory(ctx context.Context, sessionID string) (string, error) {
	var builder strings.Builder
	var rollup string
	var rolledThrough int
	if err := r.pool.QueryRow(ctx, `select content_markdown,last_decision_no from research_rollups where session_id=$1 order by version desc limit 1`, sessionID).Scan(&rollup, &rolledThrough); err == nil {
		builder.WriteString("Research rollup:\n" + rollup + "\n\n")
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	rows, err := r.pool.Query(ctx, `select decision_no,content_markdown from research_step_capsules where session_id=$1 and decision_no>$2 order by decision_no desc limit 24`, sessionID, rolledThrough)
	if err != nil {
		return "", err
	}
	type capsule struct {
		decision int
		content  string
	}
	var capsules []capsule
	for rows.Next() {
		var item capsule
		if err := rows.Scan(&item.decision, &item.content); err != nil {
			rows.Close()
			return "", err
		}
		capsules = append(capsules, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	sort.Slice(capsules, func(i, j int) bool { return capsules[i].decision < capsules[j].decision })
	for _, item := range capsules {
		fmt.Fprintf(&builder, "Step Capsule %d:\n%s\n\n", item.decision, item.content)
	}
	evidenceRows, err := r.pool.Query(ctx, `select url,coalesce(final_url,''),title,status from research_evidence_ledger where session_id=$1 order by first_seen_at,url`, sessionID)
	if err != nil {
		return "", err
	}
	var discovered, read, failed int
	readURLs := make([]string, 0)
	builder.WriteString("Evidence Ledger:\n")
	for evidenceRows.Next() {
		var url, finalURL, title, status string
		if err := evidenceRows.Scan(&url, &finalURL, &title, &status); err != nil {
			return "", err
		}
		if finalURL == "" {
			finalURL = url
		}
		switch status {
		case "read":
			read++
			if len(readURLs) < 80 {
				readURLs = append(readURLs, finalURL)
			}
		case "failed":
			failed++
		default:
			discovered++
		}
		fmt.Fprintf(&builder, "- [%s](%s) — %s\n", title, finalURL, status)
	}
	if err := evidenceRows.Err(); err != nil {
		evidenceRows.Close()
		return "", err
	}
	evidenceRows.Close()
	recentQueries, err := r.loadRecentResearchQueries(ctx, sessionID)
	if err != nil {
		return "", err
	}
	directive := buildResearchRoutingDirective(discovered, read, failed, readURLs, recentQueries)
	return directive + "\n" + compactMarkdownByParagraph(builder.String(), researchMemoryMarkdownLimit), nil
}

func (r *ResearchRuntime) loadRecentResearchQueries(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := r.pool.Query(ctx, `
		select checkpoint.payload
		from research_sessions session
		join agent_run_checkpoints checkpoint on checkpoint.run_id=session.execution_run_id
		where session.id=$1 and checkpoint.kind='action_proposal'
		order by checkpoint.sequence_no desc limit 16
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	queries := make([]string, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var proposal proposalCheckpointPayload
		if json.Unmarshal(raw, &proposal) != nil {
			continue
		}
		for _, action := range proposal.Actions {
			if action.Name != "web_search" {
				continue
			}
			var input webSearchInput
			if json.Unmarshal(action.Input, &input) != nil {
				continue
			}
			batch := strings.Join(input.Queries, " | ")
			if batch != "" && !seen[batch] {
				seen[batch] = true
				queries = append(queries, batch)
			}
		}
	}
	return queries, rows.Err()
}

func buildResearchRoutingDirective(discoveredOnly, read, failed int, readURLs, recentQueries []string) string {
	var builder strings.Builder
	total := discoveredOnly + read + failed
	fmt.Fprintf(&builder, "Execution routing constraints (authoritative current state):\n- Evidence Ledger: %d total unique URLs: %d discovered-only, %d successfully read, %d failed.\n", total, discoveredOnly, read, failed)
	if len(readURLs) > 0 {
		builder.WriteString("- Do not call `read_url` again for these successfully read URLs:\n")
		for _, url := range readURLs {
			fmt.Fprintf(&builder, "  - %s\n", url)
		}
	}
	if len(recentQueries) > 0 {
		builder.WriteString("- Do not repeat these recent `web_search` query batches; change the source family, dimension, or named implementation:\n")
		for _, query := range recentQueries {
			fmt.Fprintf(&builder, "  - %s\n", query)
		}
	}
	if discoveredOnly > read {
		builder.WriteString("- Discovery leads outnumber read evidence; prioritize substantive `read_url` calls on the strongest unread primary and independent sources before more broad discovery.\n")
	}
	builder.WriteString("- Search snippets remain discovery leads only. Continue until the accepted completion criteria are substantively met.\n")
	return builder.String()
}

func canonicalizeResearchEvidenceClaims(report string, total, discoveredOnly, read, failed int) (string, int) {
	corrections := 0
	report = chineseReadCountClaimPattern.ReplaceAllStringFunc(report, func(string) string {
		corrections++
		return fmt.Sprintf("其中 %d 个为成功读取的一手材料", read)
	})
	report = englishReadCountClaimPattern.ReplaceAllStringFunc(report, func(string) string {
		corrections++
		return fmt.Sprintf("%d successfully read sources", read)
	})
	lines := strings.Split(report, "\n")
	for index, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(line, "证据账本状态") {
			lines[index] = fmt.Sprintf("**证据账本状态（系统核验）**：共 %d 个不同 URL；%d 个仅发现、%d 个成功读取、%d 个读取失败。只有成功读取的材料可作为报告证据。", total, discoveredOnly, read, failed)
			corrections++
		} else if strings.Contains(lower, "evidence ledger status") {
			lines[index] = fmt.Sprintf("**Evidence Ledger status (system verified)**: %d unique URLs; %d discovered-only, %d successfully read, and %d failed. Only successfully read material is evidence.", total, discoveredOnly, read, failed)
			corrections++
		}
	}
	report = strings.Join(lines, "\n")
	notice := fmt.Sprintf("> **系统核验的证据账本（权威）**：共 %d 个不同 URL；%d 个仅发现、%d 个成功读取、%d 个读取失败。搜索摘要仅是线索，只有成功读取的 %d 个材料可支撑事实主张。", total, discoveredOnly, read, failed, read)
	if !containsHan(report) {
		notice = fmt.Sprintf("> **System-verified Evidence Ledger (authoritative)**: %d unique URLs; %d discovered-only, %d successfully read, and %d failed. Search snippets are leads; only the %d successfully read sources may support factual claims.", total, discoveredOnly, read, failed, read)
	}
	if firstBreak := strings.Index(report, "\n"); firstBreak >= 0 && strings.HasPrefix(strings.TrimSpace(report[:firstBreak]), "#") {
		report = report[:firstBreak+1] + "\n" + notice + "\n" + report[firstBreak+1:]
	} else {
		report = notice + "\n\n" + report
	}
	return report, corrections
}

func selectRecentResearchUnits(units []ContextUnit, keepTokens int) []ContextUnit {
	if keepTokens < 1 {
		return nil
	}
	selected := make([]ContextUnit, 0)
	used := 0
	for index := len(units) - 1; index >= 0; index-- {
		unitTokens := 0
		for _, message := range units[index].Messages {
			unitTokens += len([]rune(message.Content))/4 + 16
			for _, call := range message.ActionCalls {
				unitTokens += len(call.Input)/4 + 16
			}
		}
		if used+unitTokens > keepTokens && len(selected) > 0 {
			break
		}
		selected = append(selected, units[index])
		used += unitTokens
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func sanitizeResearchReportLinks(ctx context.Context, tx pgx.Tx, sessionID, report string) (string, []string, error) {
	rows, err := tx.Query(ctx, `select url,coalesce(final_url,'') from research_evidence_ledger where session_id=$1 and status='read'`, sessionID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	eligible := map[string]bool{}
	for rows.Next() {
		var url, finalURL string
		if err := rows.Scan(&url, &finalURL); err != nil {
			return "", nil, err
		}
		eligible[url] = true
		if finalURL != "" {
			eligible[finalURL] = true
		}
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	rewritten, removed := rewriteResearchReportLinks(report, eligible)
	return rewritten, removed, nil
}

func rewriteResearchReportLinks(report string, eligible map[string]bool) (string, []string) {
	removed := make([]string, 0)
	rewritten := markdownLinkPattern.ReplaceAllStringFunc(report, func(link string) string {
		match := markdownLinkPattern.FindStringSubmatch(link)
		if len(match) != 2 || eligible[match[1]] {
			return link
		}
		removed = append(removed, match[1])
		end := strings.Index(link, "](")
		if end < 1 {
			return link
		}
		return link[1:end]
	})
	return rewritten, removed
}
