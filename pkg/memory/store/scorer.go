package store

import (
	"context"
	"fmt"
	"math"
	"strings"

	"charm.land/fantasy"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
)

// ScoreResult is the output of scoring a piece of content.
type ScoreResult struct {
	Importance float64       // [0, 1] — how important is this to remember long-term
	Salience   float64       // [0, 1] — how relevant is this to the current conversation
	Sector     memory.Sector // classification: episodic, semantic, procedural, reflective
}

// Scorer evaluates content for memory management decisions.
type Scorer interface {
	// Score analyzes content and returns importance, salience, and sector classification.
	Score(ctx context.Context, content, role, conversationContext string) (*ScoreResult, error)
}

// --- LLM-based Scorer ---

// LLMScorer uses a language model to score memory items.
type LLMScorer struct {
	model fantasy.LanguageModel
}

// NewLLMScorer creates a scorer backed by a Fantasy LanguageModel.
func NewLLMScorer(model fantasy.LanguageModel) *LLMScorer {
	return &LLMScorer{model: model}
}

const scoringPrompt = `You are a memory scoring system. Analyze the following content and return a JSON object with exactly these fields:

- "importance": float 0.0 to 1.0. How important is this to remember long-term? High for facts, decisions, user preferences, key learnings. Low for greetings, acknowledgments, routine chat.
- "salience": float 0.0 to 1.0. How relevant is this to the current conversation context? High if directly related to the active topic.
- "sector": one of "episodic", "semantic", "procedural", "reflective".
  - "episodic": events, conversations, interactions, specific moments
  - "semantic": facts, knowledge, concepts, definitions
  - "procedural": how-to, workflows, patterns, instructions
  - "reflective": meta-observations, self-assessments, reasoning about reasoning

Content (role=%s):
%s

Conversation context (last few messages):
%s

Return ONLY valid JSON. No explanation.`

func (s *LLMScorer) Score(ctx context.Context, content, role, conversationContext string) (*ScoreResult, error) {
	prompt := fmt.Sprintf(scoringPrompt, role, content, conversationContext)

	temp := float64(0.1)
	maxTokens := int64(256)

	resp, err := s.model.Generate(ctx, fantasy.Call{
		Prompt: fantasy.Prompt{
			fantasy.NewUserMessage(prompt),
		},
		Temperature:     &temp,
		MaxOutputTokens: &maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("llm scoring call: %w", err)
	}

	text := resp.Content.Text()
	return parseScoringResponse(text)
}

func parseScoringResponse(text string) (*ScoreResult, error) {
	var raw struct {
		Importance float64 `json:"importance"`
		Salience   float64 `json:"salience"`
		Sector     string  `json:"sector"`
	}
	opts := &security.ExtractJSONOptions{
		MaxInputBytes:         16 * 1024, // scoring responses should be tiny
		DisallowUnknownFields: true,
	}
	if err := security.ExtractJSON(text, &raw, opts); err != nil {
		return nil, fmt.Errorf("parse scoring response: %w (raw: %s)", err, text)
	}

	return &ScoreResult{
		Importance: clamp01(raw.Importance),
		Salience:   clamp01(raw.Salience),
		Sector:     normalizeSector(raw.Sector),
	}, nil
}

// --- Heuristic Scorer (no LLM, rule-based fallback) ---

// HeuristicScorer uses simple rules to classify and score content.
// Useful when no LLM is available or for fast-path decisions.
type HeuristicScorer struct{}

// NewHeuristicScorer creates a rule-based scorer.
func NewHeuristicScorer() *HeuristicScorer {
	return &HeuristicScorer{}
}

func (s *HeuristicScorer) Score(_ context.Context, content, role, _ string) (*ScoreResult, error) {
	result := &ScoreResult{
		Importance: s.estimateImportance(content, role),
		Salience:   0.5, // heuristic can't assess conversational salience
		Sector:     s.classifySector(content),
	}
	return result, nil
}

func (s *HeuristicScorer) estimateImportance(content, role string) float64 {
	lower := strings.ToLower(content)
	score := 0.3 // baseline

	// Length signal: longer content tends to carry more information
	tokens := float64(len(content)) / 4
	if tokens > 100 {
		score += 0.1
	}
	if tokens > 500 {
		score += 0.1
	}

	// Role signals
	switch role {
	case "system":
		score += 0.2 // system messages are typically important
	case "tool":
		score += 0.15 // tool results carry information
	case "assistant":
		score += 0.05
	}

	// Content signals — keywords indicating importance
	importantKeywords := []string{
		"remember", "important", "key", "critical", "decision",
		"preference", "always", "never", "rule", "requirement",
		"config", "password", "api key", "secret", "credential",
		"deadline", "milestone", "goal", "budget", "cost",
	}
	for _, kw := range importantKeywords {
		if strings.Contains(lower, kw) {
			score += 0.1
			break
		}
	}

	// Low-importance signals
	lowKeywords := []string{
		"hello", "hi", "thanks", "thank you", "bye", "ok",
		"sure", "got it", "yes", "no", "understood",
	}
	isLowOnly := true
	for _, kw := range lowKeywords {
		if lower == kw || lower == kw+"." || lower == kw+"!" {
			score -= 0.2
		}
		if !strings.Contains(lower, kw) {
			isLowOnly = false
		}
	}
	_ = isLowOnly

	// Code block signal
	if strings.Contains(content, "```") {
		score += 0.15
	}

	return clamp01(score)
}

func (s *HeuristicScorer) classifySector(content string) memory.Sector {
	lower := strings.ToLower(content)

	// Procedural indicators
	proceduralKeywords := []string{
		"step", "how to", "install", "run", "execute", "command",
		"workflow", "process", "procedure", "recipe", "instructions",
		"first", "then", "finally", "next",
	}
	proceduralScore := 0
	for _, kw := range proceduralKeywords {
		if strings.Contains(lower, kw) {
			proceduralScore++
		}
	}

	// Semantic indicators
	semanticKeywords := []string{
		"is", "means", "definition", "concept", "fact",
		"because", "therefore", "api", "interface", "struct",
		"type", "function", "class", "module",
	}
	semanticScore := 0
	for _, kw := range semanticKeywords {
		if strings.Contains(lower, kw) {
			semanticScore++
		}
	}

	// Reflective indicators
	reflectiveKeywords := []string{
		"i think", "i believe", "in my opinion", "reflection",
		"lesson learned", "takeaway", "insight", "realization",
		"observation", "pattern",
	}
	reflectiveScore := 0
	for _, kw := range reflectiveKeywords {
		if strings.Contains(lower, kw) {
			reflectiveScore++
		}
	}

	// Default to episodic, pick the highest scoring alternative
	maxScore := proceduralScore
	sector := memory.SectorProcedural

	if semanticScore > maxScore {
		maxScore = semanticScore
		sector = memory.SectorSemantic
	}
	if reflectiveScore > maxScore {
		maxScore = reflectiveScore
		sector = memory.SectorReflective
	}

	// If no strong signal, default to episodic
	if maxScore < 2 {
		return memory.SectorEpisodic
	}
	return sector
}

// --- helpers ---

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

func normalizeSector(s string) memory.Sector {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "episodic":
		return memory.SectorEpisodic
	case "semantic":
		return memory.SectorSemantic
	case "procedural":
		return memory.SectorProcedural
	case "reflective":
		return memory.SectorReflective
	default:
		return memory.SectorEpisodic
	}
}
