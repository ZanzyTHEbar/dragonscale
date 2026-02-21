package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory/observation"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/skills"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
)

type ContextBuilder struct {
	workspace        string
	skillsLoader     *skills.SkillsLoader
	memoryStore      memory.Memory         // 3-tier MemGPT memory (may be nil)
	delegate         memory.MemoryDelegate // Direct delegate for document loading (may be nil)
	tools            *tools.ToolRegistry   // Direct reference to tool registry
	observationBlock string                // Pre-rendered observation block for prompt injection
	knowledgeBlock   string                // Pre-rendered knowledge block from Focus completions
	dagBlock         string                // Pre-rendered DAG compressed history
	contextWindow    int                   // Max tokens for context window (0 = no limit)
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// Primary skills dir: XDG data dir (installed skills).
	// Falls back to workspace/skills for legacy setups.
	primarySkillsDir := filepath.Join(workspace, "skills")
	if dir, err := config.SkillsDir(); err == nil {
		primarySkillsDir = dir
	}

	// Global skills: ~/.config/dragonscale/skills (user-level overrides).
	globalSkillsDir := ""
	if dir, err := config.ConfigDir(); err == nil {
		globalSkillsDir = filepath.Join(dir, "skills")
	}

	// Builtin skills: skills/ directory relative to the binary's working dir.
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(primarySkillsDir, globalSkillsDir, builtinSkillsDir),
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

// SetContextWindow configures the token budget for the system prompt.
func (cb *ContextBuilder) SetContextWindow(tokens int) {
	cb.contextWindow = tokens
}

func (cb *ContextBuilder) getIdentity() string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# dragonscale 🦞

You are dragonscale, a helpful AI assistant.

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

4. **No fabricated data** - If access is denied, a tool fails, or a request is outside workspace/sandbox, explicitly say so. Do NOT invent file contents, command output, credentials, or sample sensitive data.

5. **Completion discipline** - For actionable requests, execute the required tools before your final answer. Do NOT end with only intent statements like "I'll do that" or "let me do that."

6. **Context Management** - You MUST consolidate your context to stay effective during long tasks. Use start_focus at the beginning of any investigation or multi-step task. After 10-15 tool calls, call complete_focus with a summary of what you learned and accomplished. This compresses your working context and persists knowledge for future reference. Failing to consolidate will degrade your performance as context grows.`,
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
	type section struct {
		name     string
		content  string
		priority int // lower = higher priority (kept first when trimming)
	}

	// Collect sections in priority order
	sections := []section{}

	// P0: Core identity (always included)
	sections = append(sections, section{"identity", cb.getIdentity(), 0})

	// P1: Bootstrap files (user identity)
	if bc := cb.LoadBootstrapFiles(); bc != "" {
		sections = append(sections, section{"bootstrap", bc, 1})
	}

	// P2: Skills index (lightweight Level 1 metadata)
	if summary := cb.skillsLoader.BuildSkillsSummary(); summary != "" {
		sections = append(sections, section{"skills", fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill:
1. Use **skill_search** to find relevant skills by keyword
2. Call **skill_read** directly to load the full skill content
3. Call **skill_traverse** directly to explore related skills

Do NOT assume skill content — always load before applying.

%s`, summary), 2})
	}

	// P3: Working context (hot tier — highly dynamic, high value)
	if cb.memoryStore != nil {
		if wc := cb.buildWorkingContextSection(); wc != "" {
			sections = append(sections, section{"working_context", wc, 3})
		}
	}

	// P4: Observation block
	if cb.observationBlock != "" {
		sections = append(sections, section{"observations", "# Observations\n\n" + cb.observationBlock, 4})
	}

	// P5: Knowledge block
	if cb.knowledgeBlock != "" {
		sections = append(sections, section{"knowledge", cb.knowledgeBlock, 5})
	}

	// P6: DAG compressed history (lowest priority — can be reconstructed)
	if cb.dagBlock != "" {
		sections = append(sections, section{"dag", "# Conversation History (Compressed)\n\n" + cb.dagBlock, 6})
	}

	// Token budget enforcement: if we exceed ~40% of context window for the
	// system prompt, trim lowest-priority sections first.
	budgetTokens := cb.tokenBudgetTokens()
	totalTokens := 0
	sectionTokens := make([]int, len(sections))
	for i, s := range sections {
		sectionTokens[i] = observation.EstimateTokens(s.content)
		totalTokens += sectionTokens[i]
	}

	if budgetTokens > 0 && totalTokens > budgetTokens {
		logger.WarnCF("context", "System prompt exceeds token budget, trimming low-priority sections",
			map[string]interface{}{
				"total_tokens":  totalTokens,
				"budget_tokens": budgetTokens,
				"sections":      len(sections),
			})
		// Trim from lowest priority (highest number) first
		for i := len(sections) - 1; i >= 0 && totalTokens > budgetTokens; i-- {
			if sections[i].priority >= 5 { // only trim P5+ (knowledge, dag)
				totalTokens -= sectionTokens[i]
				sections[i].content = ""
			}
		}
	}

	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		if s.content != "" {
			parts = append(parts, s.content)
		}
	}

	prompt := strings.Join(parts, "\n\n---\n\n")

	// Log token estimate for observability
	tokenEst := observation.EstimateTokens(prompt)
	logger.DebugCF("context", "System prompt token estimate",
		map[string]interface{}{
			"chars":      len(prompt),
			"tokens_est": tokenEst,
			"sections":   len(parts),
		})

	return prompt
}

// tokenBudgetTokens returns the maximum token count for the system prompt,
// derived from the context window size. Returns 0 if no limit is configured.
func (cb *ContextBuilder) tokenBudgetTokens() int {
	if cb.contextWindow <= 0 {
		return 0
	}
	// Reserve ~40% of context window for system prompt
	return int(float64(cb.contextWindow) * 0.4)
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	if cb.delegate == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	docs, err := cb.delegate.ListDocumentsByCategory(ctx, pkg.NAME, "bootstrap")
	if err != nil || len(docs) == 0 {
		return ""
	}

	var result string
	for _, doc := range docs {
		result += fmt.Sprintf("## %s\n\n%s\n\n", doc.Name, doc.Content)
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
	wc, err := cb.memoryStore.GetWorkingContext(ctx, pkg.NAME, "default")
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

// GetSkillsInfo returns information about available skills (metadata only).
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
