package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/huangxinxinyu/nano-notebook/internal/agentcatalog"
	"github.com/huangxinxinyu/nano-notebook/internal/models"
	"github.com/huangxinxinyu/nano-notebook/internal/skillcatalog"
)

type readSkillAction struct {
	definitions agentcatalog.Catalog
	skills      skillcatalog.Catalog
}

type readSkillInput struct {
	Skill agentcatalog.Reference `json:"skill"`
}

type readSkillOutput struct {
	Skill        string `json:"skill"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	SHA256       string `json:"sha256"`
}

func NewReadSkillAction(definitions agentcatalog.Catalog, skills skillcatalog.Catalog) Action {
	return &readSkillAction{definitions: definitions, skills: skills}
}

func (*readSkillAction) Definition() models.ActionDefinition {
	return models.ActionDefinition{
		Name:        "read_skill",
		Description: "Load the full instructions for one exact Skill version already allowed by this Agent Definition.",
		InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["skill"],"properties":{"skill":{"type":"string","pattern":"^[a-z0-9._-]+@[1-9][0-9]*$"}}}`),
	}
}

func (*readSkillAction) ValidateInput(raw json.RawMessage) error {
	_, err := decodeReadSkillInput(raw)
	return err
}

func (a *readSkillAction) Execute(ctx context.Context, request ActionRequest) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	input, err := decodeReadSkillInput(request.Input)
	if err != nil {
		return ActionResult{}, err
	}
	definition, ok := a.definitions.ResolveDefinition(request.Definition)
	if !ok || !containsReference(definition.Skills, input.Skill) {
		return ActionResult{Status: ActionDomainError, ErrorCode: "skill_not_allowed"}, nil
	}
	skill, ok := a.skills.Resolve(input.Skill.Identity, input.Skill.Version)
	if !ok {
		return ActionResult{Status: ActionDomainError, ErrorCode: "skill_not_found"}, nil
	}
	payload, err := json.Marshal(readSkillOutput{
		Skill: input.Skill.String(), Name: skill.Name, Description: skill.Description,
		Instructions: skill.Body, SHA256: skill.SHA256,
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Status: ActionSucceeded, Output: payload}, nil
}

func decodeReadSkillInput(raw json.RawMessage) (readSkillInput, error) {
	var input readSkillInput
	if err := decodeExactJSON(raw, &input); err != nil {
		return readSkillInput{}, errors.New("invalid read_skill input")
	}
	return input, nil
}

func containsReference(values []agentcatalog.Reference, target agentcatalog.Reference) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
