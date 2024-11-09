package llmpipeline

import (
	"fmt"

	"github.com/ZanzyTHEbar/decision-engine-go/llmpipeline/decisionengine"
	"github.com/ZanzyTHEbar/decision-engine-go/llmpipeline/queryprocessor"
)

type LLMPipeline struct {
	QueryProcessor queryprocessor.QueryProcessor
	DecisionEngine decisionengine.DecisionEngine
}

// If providede with a nil QueryProcessor or DecisionEngine, the function will create a new instance of the respective struct using the default constructor.
func NewLLMPipeline(queryProcessor queryprocessor.QueryProcessor, decisionEngine decisionengine.DecisionEngine) *LLMPipeline {
	return &LLMPipeline{
		QueryProcessor: queryprocessor.NewQueryProcessor(queryProcessor),
		DecisionEngine: decisionengine.NewDecisionEngine(decisionEngine),
	}
}

// ProcessQuery - Main entry point for the package
func (llm *LLMPipeline) ProcessQuery(query string) (string, error) {
	// Phase 1: Query Processor
	refinedQuery, err := llm.QueryProcessor.Process(query)
	if err != nil {
		return "", fmt.Errorf("failed to process query: %w", err)
	}

	// Phase 2: Decision Engine
	finalAnswer, err := llm.DecisionEngine.Execute(refinedQuery)
	if err != nil {
		return "", fmt.Errorf("failed to execute decision engine: %w", err)
	}

	return finalAnswer, nil
}
