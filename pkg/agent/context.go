package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/messages"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type ContextBuilder struct {
	workspace       string
	skillsLoader    *skills.SkillsLoader
	memoryStore     memory.Memory         // 3-tier MemGPT memory (may be nil)
	delegate        memory.MemoryDelegate // Direct delegate for document loading (may be nil)
	tools           *tools.ToolRegistry   // Direct reference to tool registry
	observationBlock string               // Pre-rendered observation block for prompt injection
	knowledgeBlock   string               // Pre-rendered knowledge block from Focus completions
	dagBlock         string               // Pre-rendered DAG compressed history
}

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".picoclaw")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

// SetMemoryStore sets the 3-tier MemGPT memory store for working context injection.
func (cb *ContextBuilder) SetMemoryStore(ms memory.Memory) {
	cb.memoryStore = ms
}

// SetDelegate sets the memory delegate for document loading.
func (cb *ContextBuilder) SetDelegate(del memory.MemoryDelegate) {
	cb.delegate = del
}

func (cb *ContextBuilder) SkillsLoader() *skills.SkillsLoader {
	return cb.skillsLoader
}

func (cb *ContextBuilder) SetObservationBlock(block string) {
	cb.observationBlock = block
}

// SetKnowledgeBlock sets the pre-rendered knowledge block from completed Focus sessions.
func (cb *ContextBuilder) SetKnowledgeBlock(block string) {
	cb.knowledgeBlock = block
}

// SetDAGBlock sets the pre-rendered DAG compressed history for prompt injection.
func (cb *ContextBuilder) SetDAGBlock(block string) {
	cb.dagBlock = block
}

func (cb *ContextBuilder) getIdentity() string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# picoclaw 🦞

You are picoclaw, a helpful AI assistant.

## Current Time
%s

## Runtime
%s

## Workspace
Your workspace is at: %s
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Important Rules

1. **ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

3. **Memory** - Use the memory tool to store important facts, preferences, and decisions.

4. **Context Management** - You MUST consolidate your context to stay effective during long tasks. Use start_focus at the beginning of any investigation or multi-step task. After 10-15 tool calls, call complete_focus with a summary of what you learned and accomplished. This compresses your working context and persists knowledge for future reference. Failing to consolidate will degrade your performance as context grows.`,
		now, runtime, workspacePath, workspacePath, toolsSection)
}

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n")
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	parts := []string{}

	// Core identity section
	parts = append(parts, cb.getIdentity())

	// Bootstrap files
	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills - show summary, AI can read full content with read_file tool
	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.

%s`, skillsSummary))
	}

	// Observation block (stable prefix for prompt cache alignment)
	if cb.observationBlock != "" {
		parts = append(parts, "# Observations\n\n"+cb.observationBlock)
	}

	if cb.knowledgeBlock != "" {
		parts = append(parts, cb.knowledgeBlock)
	}

	if cb.dagBlock != "" {
		parts = append(parts, "# Conversation History (Compressed)\n\n"+cb.dagBlock)
	}

	// 3-tier MemGPT working context injection
	if cb.memoryStore != nil {
		wcSection := cb.buildWorkingContextSection()
		if wcSection != "" {
			parts = append(parts, wcSection)
		}
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	if cb.delegate != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		docs, err := cb.delegate.ListDocumentsByCategory(ctx, "picoclaw", "bootstrap")
		if err == nil && len(docs) > 0 {
			var result string
			for _, doc := range docs {
				result += fmt.Sprintf("## %s\n\n%s\n\n", doc.Name, doc.Content)
			}
			return result
		}
	}

	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
	}

	var result string
	for _, filename := range bootstrapFiles {
		filePath := filepath.Join(cb.workspace, filename)
		if data, err := os.ReadFile(filePath); err == nil {
			result += fmt.Sprintf("## %s\n\n%s\n\n", filename, string(data))
		}
	}

	return result
}

// buildWorkingContextSection returns the working context section for the system prompt.
// It includes the hot-tier working context buffer and memory usage instructions.
func (cb *ContextBuilder) buildWorkingContextSection() string {
	if cb.memoryStore == nil {
		return ""
	}

	// Use a background context for system prompt building (non-blocking)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var parts []string

	// Inject working context (hot tier)
	wc, err := cb.memoryStore.GetWorkingContext(ctx, "picoclaw", "default")
	if err == nil && wc != "" {
		parts = append(parts, "## Working Context\n\n"+wc)
	}

	// Memory system instructions
	parts = append(parts, `## Memory System

You have a 3-tier memory system accessible via the **memory** tool:

- **Working Context** (hot): Always loaded. Use memory tool to update it with durable facts.
- **Recall** (warm): Scored memory items. Search returns the most relevant.
- **Archival** (cold): Large documents chunked and embedded for retrieval.

Use the memory tool to:
- **search**: Find relevant memories across all tiers.
- **write**: Store important facts, preferences, decisions.
- **status**: Check memory pressure and usage.

Store important user preferences, key decisions, and facts you want to remember long-term.`)

	return strings.Join(parts, "\n\n")
}

func (cb *ContextBuilder) BuildMessages(history []messages.Message, summary string, currentMessage string, media []string, channel, chatID string) []messages.Message {
	msgs := []messages.Message{}

	systemPrompt := cb.BuildSystemPrompt()

	// Add Current Session info if provided
	if channel != "" && chatID != "" {
		systemPrompt += fmt.Sprintf("\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	// Log system prompt summary for debugging (debug mode only)
	logger.DebugCF("agent", "System prompt built",
		map[string]interface{}{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview",
		map[string]interface{}{
			"preview": preview,
		})

	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	// Note: Orphaned tool messages are now prevented at the source by
	// tool-call-aware truncation in SessionManager.TruncateHistory().

	msgs = append(msgs, messages.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	msgs = append(msgs, history...)

	msgs = append(msgs, messages.Message{
		Role:    "user",
		Content: currentMessage,
	})

	return msgs
}

func (cb *ContextBuilder) AddToolResult(msgs []messages.Message, toolCallID, toolName, result string) []messages.Message {
	msgs = append(msgs, messages.Message{
		Role:       "tool",
		Content:    result,
		ToolCallID: toolCallID,
	})
	return msgs
}

func (cb *ContextBuilder) AddAssistantMessage(msgs []messages.Message, content string, toolCalls []map[string]interface{}) []messages.Message {
	msg := messages.Message{
		Role:    "assistant",
		Content: content,
	}
	// Always add assistant message, whether or not it has tool calls
	msgs = append(msgs, msg)
	return msgs
}

func (cb *ContextBuilder) loadSkills() string {
	allSkills := cb.skillsLoader.ListSkills()
	if len(allSkills) == 0 {
		return ""
	}

	var skillNames []string
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}

	content := cb.skillsLoader.LoadSkillsForContext(skillNames)
	if content == "" {
		return ""
	}

	return "# Skill Definitions\n\n" + content
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]interface{} {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]interface{}{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}
