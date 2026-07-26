package agent

import "time"

type RunConfig struct {
	ID                     string        `json:"id"`
	ExecutorVersion        string        `json:"executor_version"`
	ActionDecisionLimit    int           `json:"action_decision_limit"`
	FinalDecisionLimit     int           `json:"final_decision_limit"`
	ActionLimit            int           `json:"action_limit"`
	ActionBatchLimit       int           `json:"action_batch_limit"`
	ActionResultByteLimit  int           `json:"action_result_byte_limit"`
	ActionResultsByteLimit int           `json:"action_results_byte_limit"`
	Deadline               time.Duration `json:"deadline"`
}

func DefaultRunConfig(id string) RunConfig {
	if id == "" {
		id = "nano-interactive-v1"
	}
	return RunConfig{
		ID: id, ExecutorVersion: "leader-executor-v1", ActionDecisionLimit: 4, FinalDecisionLimit: 1,
		ActionLimit: 8, ActionBatchLimit: 4, ActionResultByteLimit: 16 * 1024,
		ActionResultsByteLimit: 64 * 1024, Deadline: 10 * time.Minute,
	}
}
