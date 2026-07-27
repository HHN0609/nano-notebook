package agentcatalog

import (
	"errors"
	"fmt"
	"strconv"
)

type ToolScheduling string

const (
	ToolOrderedSync ToolScheduling = "ordered_sync"
)

type ToolCapability struct {
	Scheduling ToolScheduling
}

type ExecutorCapability struct {
	PromptPurposes map[string]bool
	Tools          map[string]bool
	ChildExecutors map[string]bool
	MaxLimits      Limits
	MaxChildren    int
}

type Bindings struct {
	Prompts   map[Reference]bool
	Tools     map[string]ToolCapability
	Executors map[string]ExecutorCapability
}

func (c Catalog) ValidateBindings(bindings Bindings) error {
	for reference, definition := range c.definitions {
		capability, ok := bindings.Executors[definition.Executor]
		if !ok {
			return fmt.Errorf("definition %s references unknown executor %q", reference, definition.Executor)
		}
		if err := validateLimitCeiling(definition.Limits, capability.MaxLimits); err != nil {
			return fmt.Errorf("definition %s exceeds executor limits: %w", reference, err)
		}
		if len(definition.Children) > capability.MaxChildren {
			return fmt.Errorf("definition %s exceeds executor child capability", reference)
		}
		for purpose, prompt := range definition.Prompts {
			if !capability.PromptPurposes[purpose] {
				return fmt.Errorf("definition %s uses unsupported prompt purpose %q", reference, purpose)
			}
			if !bindings.Prompts[prompt] {
				return fmt.Errorf("definition %s references unbound prompt %s", reference, prompt)
			}
		}
		for _, tool := range definition.Tools {
			toolCapability, exists := bindings.Tools[tool]
			if !exists || !capability.Tools[tool] {
				return fmt.Errorf("definition %s references unbound tool %q", reference, tool)
			}
			if toolCapability.Scheduling != ToolOrderedSync {
				return fmt.Errorf("tool %q has unsupported scheduling %q", tool, toolCapability.Scheduling)
			}
		}
		for _, child := range definition.Children {
			childDefinition := c.definitions[child]
			if !capability.ChildExecutors[childDefinition.Executor] {
				return fmt.Errorf("definition %s cannot delegate to executor %q", reference, childDefinition.Executor)
			}
		}
	}
	return nil
}

func DelegationToolName(reference Reference) (string, error) {
	if err := validateReference(reference); err != nil {
		return "", err
	}
	name := "delegate." + reference.Identity + ".v" + strconv.Itoa(reference.Version)
	if !validToolName(name) {
		return "", fmt.Errorf("generated MCP tool name %q is invalid or exceeds %d bytes", name, maxMCPToolNameLength)
	}
	return name, nil
}

func validateLimitCeiling(value, ceiling Limits) error {
	if err := validatePositiveLimits(value); err != nil {
		return err
	}
	checks := []struct {
		name           string
		value, ceiling int
	}{
		{"model_calls", value.ModelCalls, ceiling.ModelCalls},
		{"actions", value.Actions, ceiling.Actions},
		{"action_batch", value.ActionBatch, ceiling.ActionBatch},
		{"context_bytes", value.ContextBytes, ceiling.ContextBytes},
		{"result_bytes", value.ResultBytes, ceiling.ResultBytes},
		{"attempts", value.Attempts, ceiling.Attempts},
	}
	for _, check := range checks {
		if check.ceiling < 1 {
			return errors.New("executor capability ceiling is incomplete")
		}
		if check.value > check.ceiling {
			return fmt.Errorf("%s=%d exceeds %d", check.name, check.value, check.ceiling)
		}
	}
	return nil
}
