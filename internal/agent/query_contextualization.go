package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

const (
	composerHistoryPairLimit        = 3
	composerHistoryTotalRuneLimit   = 4000
	composerHistoryMessageRuneLimit = 1200
	composerCurrentRuneLimit        = 4000
	searchQueryRuneLimit            = 2000
)

type completedConversationPair struct {
	user      string
	assistant string
}

// buildGroundedComposerRequest builds the single free-choice decision request
// for a Sources-selected chat turn. It carries a small bounded window of
// recently completed turns (reference-only, for resolving pronouns/ellipsis/
// omitted subjects in the current Message) plus the current Message itself
// (authoritative) — the same inputs the isolated query-contextualizer used to
// receive, now folded into the ordinary decision loop instead of a separate
// forced-first-action call. See
// docs/superpowers/specs/2026-08-04-prompt-driven-leader-decision-loop-design.md.
func (r *PostgresRuntime) buildGroundedComposerRequest(ctx context.Context, execution Execution) (models.ModelRequest, error) {
	current, pairs, err := r.loadCompletedConversation(ctx, execution)
	if err != nil {
		return models.ModelRequest{}, err
	}
	current = strings.TrimSpace(current)
	if current == "" || !utf8.ValidString(current) {
		return models.ModelRequest{}, errors.New("current Message is invalid")
	}
	systemPrompt := r.systemPrompt
	if systemPrompt == BareSystemPrompt {
		systemPrompt = GroundedSystemPrompt
	}
	boundedPairs := boundCompletedPairs(pairs)
	var prompt strings.Builder
	prompt.WriteString("RECENT COMPLETED CONTEXT (reference only):\n")
	if len(boundedPairs) == 0 {
		prompt.WriteString("(none)\n")
	} else {
		for index, pair := range boundedPairs {
			_, _ = fmt.Fprintf(&prompt, "Pair %d user: %s\nPair %d assistant: %s\n", index+1, pair.user, index+1, pair.assistant)
		}
	}
	prompt.WriteString("\nCURRENT MESSAGE (authoritative):\n")
	prompt.WriteString(truncateRunes(current, composerCurrentRuneLimit))
	messages := []models.ModelMessage{
		{Role: models.RoleSystem, Content: systemPrompt},
		{Role: models.RoleUser, Content: prompt.String()},
	}
	return models.ModelRequest{Model: execution.Model, Messages: messages}, nil
}

func (r *PostgresRuntime) loadCompletedConversation(ctx context.Context, execution Execution) (string, []completedConversationPair, error) {
	tx, err := r.workerTx(ctx)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx)
	var current string
	if err := tx.QueryRow(ctx, `
		select content from chat_messages where id=$2 and chat_id=$1 and role='user'
	`, execution.ChatID, execution.InputMessageID).Scan(&current); err != nil {
		return "", nil, err
	}
	rows, err := tx.Query(ctx, `
		with cutoff as (
			select created_at,id from chat_messages where id=$2 and chat_id=$1
		)
		select input.content,output.content
		from agent_runs prior
		left join chat_runs product on product.root_agent_run_id=prior.id
		join chat_messages input on input.id=coalesce(prior.input_message_id,product.input_message_id)
			and input.chat_id=coalesce(prior.chat_id,product.chat_id) and input.role='user'
		join chat_messages output on output.id=coalesce(prior.output_message_id,product.output_message_id)
			and output.chat_id=coalesce(prior.chat_id,product.chat_id) and output.role='assistant'
		cross join cutoff
		where coalesce(prior.chat_id,product.chat_id)=$1 and prior.status='completed'
			and (input.created_at,input.id)<(cutoff.created_at,cutoff.id)
		order by input.created_at desc,input.id desc
		limit $3
	`, execution.ChatID, execution.InputMessageID, composerHistoryPairLimit)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	pairsNewestFirst := make([]completedConversationPair, 0, composerHistoryPairLimit)
	for rows.Next() {
		var pair completedConversationPair
		if err := rows.Scan(&pair.user, &pair.assistant); err != nil {
			return "", nil, err
		}
		pairsNewestFirst = append(pairsNewestFirst, pair)
	}
	if err := rows.Err(); err != nil {
		return "", nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", nil, err
	}
	pairs := make([]completedConversationPair, len(pairsNewestFirst))
	for index := range pairsNewestFirst {
		pairs[len(pairsNewestFirst)-1-index] = pairsNewestFirst[index]
	}
	return current, pairs, nil
}

func boundCompletedPairs(pairs []completedConversationPair) []completedConversationPair {
	if len(pairs) > composerHistoryPairLimit {
		pairs = pairs[len(pairs)-composerHistoryPairLimit:]
	}
	remaining := composerHistoryTotalRuneLimit
	newestFirst := make([]completedConversationPair, 0, len(pairs))
	for index := len(pairs) - 1; index >= 0 && remaining > 0; index-- {
		user := truncateRunes(strings.TrimSpace(pairs[index].user), minInt(composerHistoryMessageRuneLimit, remaining))
		remaining -= utf8.RuneCountInString(user)
		assistant := truncateRunes(strings.TrimSpace(pairs[index].assistant), minInt(composerHistoryMessageRuneLimit, remaining))
		remaining -= utf8.RuneCountInString(assistant)
		if user == "" || assistant == "" {
			continue
		}
		newestFirst = append(newestFirst, completedConversationPair{user: user, assistant: assistant})
	}
	bounded := make([]completedConversationPair, len(newestFirst))
	for index := range newestFirst {
		bounded[len(newestFirst)-1-index] = newestFirst[index]
	}
	return bounded
}

// preserveCurrentSearchQuery is used by acceptContextualizedSearch (see
// controller.go) for runtimes that implement QueryContextRuntime — today,
// only Studio Output generation. It guards against a model-authored query
// silently dropping the deterministic fallback query's terms.
func preserveCurrentSearchQuery(proposal, fallback models.ActionProposal) (models.ActionProposal, error) {
	var current searchEvidenceInput
	if err := json.Unmarshal(fallback.Input, &current); err != nil {
		return models.ActionProposal{}, err
	}
	var contextualized searchEvidenceInput
	if err := json.Unmarshal(proposal.Input, &contextualized); err != nil {
		return models.ActionProposal{}, err
	}
	if !strings.Contains(contextualized.Query, current.Query) {
		contextualized.Query = truncateRunes(strings.TrimSpace(current.Query+" "+contextualized.Query), searchQueryRuneLimit)
	}
	input, err := json.Marshal(contextualized)
	if err != nil {
		return models.ActionProposal{}, err
	}
	proposal.Input = input
	return proposal, nil
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
