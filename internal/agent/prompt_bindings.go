package agent

import "github.com/huangxinxinyu/nano-notebook/internal/promptcatalog"

var productionPromptCatalog = promptcatalog.MustLoadEmbedded()

var (
	BareSystemPrompt     = mustPromptContent("agent.chat-composer-bare", 2)
	GroundedSystemPrompt = mustPromptContent("agent.chat-composer-grounded", 3)
)

func mustPromptContent(identity string, version int) string {
	prompt, ok := productionPromptCatalog.Resolve(identity, version)
	if !ok {
		panic("missing embedded prompt " + identity)
	}
	return prompt.Content
}

func promptTraceRef(identity string, version int) PromptVersionRef {
	prompt, ok := productionPromptCatalog.Resolve(identity, version)
	if !ok {
		panic("missing embedded prompt " + identity)
	}
	return PromptVersionRef{Identity: prompt.Identity, Version: prompt.Version, Contract: prompt.Contract, SHA256: prompt.SHA256}
}

func composerPromptTraceRef(promptVersion string) PromptVersionRef {
	if promptVersion == GroundedPromptVersion {
		return promptTraceRef("agent.chat-composer-grounded", 3)
	}
	return promptTraceRef("agent.chat-composer-bare", 2)
}
