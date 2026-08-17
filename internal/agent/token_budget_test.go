package agent

import (
	"encoding/json"
	"testing"

	"github.com/huangxinxinyu/nano-notebook/internal/models"
)

func TestEstimateModelRequestTokensIncludesAllProviderInputs(t *testing.T) {
	base := models.ModelRequest{Model: "provider/model", Messages: []models.ModelMessage{
		{Role: models.RoleSystem, Content: "system contract"},
		{Role: models.RoleUser, Content: "<summary>prior work</summary>"},
		{Role: models.RoleUser, Content: "current request"},
	}}
	baseCount, err := EstimateModelRequestTokens(base)
	if err != nil {
		t.Fatal(err)
	}
	withTool := base
	withTool.ActionDefinitions = []models.ActionDefinition{{
		Name: "search_evidence", Description: "search pinned evidence",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
	}}
	toolCount, err := EstimateModelRequestTokens(withTool)
	if err != nil {
		t.Fatal(err)
	}
	withEvidence := withTool
	withEvidence.Messages = append(append([]models.ModelMessage(nil), withTool.Messages...),
		models.ModelMessage{Role: models.RoleAssistant, ActionCalls: []models.ModelActionCall{{
			ID: "decision:1/action:0", Name: "search_evidence", Input: json.RawMessage(`{"query":"q"}`),
		}}},
		models.ModelMessage{Role: models.RoleAction, ActionCallID: "decision:1/action:0", Content: `{"status":"succeeded","output":{"evidence":[{"text":"grounded fact"}]}}`},
	)
	evidenceCount, err := EstimateModelRequestTokens(withEvidence)
	if err != nil {
		t.Fatal(err)
	}
	if baseCount.Source != TokenCountEstimated || baseCount.Tokens < 1 || toolCount.Tokens <= baseCount.Tokens || evidenceCount.Tokens <= toolCount.Tokens {
		t.Fatalf("base=%+v tool=%+v evidence=%+v", baseCount, toolCount, evidenceCount)
	}
}

func TestEstimateModelRequestTokensIsDeterministic(t *testing.T) {
	request := models.ModelRequest{Model: "provider/model", Messages: []models.ModelMessage{{Role: models.RoleUser, Content: "你好，world"}}}
	first, err := EstimateModelRequestTokens(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EstimateModelRequestTokens(request)
	if err != nil || first != second {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
}
