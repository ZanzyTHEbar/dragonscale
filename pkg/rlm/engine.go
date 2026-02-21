package rlm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/itr"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
)

// CallModelFunc is the signature for making a direct LLM call.
// The RLMEngine uses this for the leaf-level answer when the context fits
// within the direct threshold, and for merge synthesis.
type CallModelFunc func(ctx context.Context, systemPrompt, userQuery string) (string, uint32, error)

// EngineConfig configures the RLMEngine.
type EngineConfig struct {
	Strategy       StrategyConfig
	MaxConcurrency int // goroutines per fan-out level; 0 = unbounded
}

// DefaultEngineConfig returns sensible defaults.
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		Strategy:       DefaultStrategyConfig(),
		MaxConcurrency: 4,
	}
}

// Engine is the RLM recursive decomposition engine. It processes arbitrarily
// long contexts by recursively partitioning them and answering sub-queries,
// all mediated through the SecureBus for capability enforcement and leak scanning.
//
// Architecture (from ADR-001):
//
//	Query + Context
//	     │
//	     ▼
//	StrategyPlanner.PlanNext()
//	     │
//	     ├── OpFinal  → callModel(context, query) → answer
//	     ├── OpGrep   → grep context, recurse over matches
//	     └── OpPartition → split into k partitions
//	               │
//	               └── FanOut → k × Engine.Recurse (depth+1)
//	                                │
//	                                └── MergeResults → final answer
type Engine struct {
	cfg       EngineConfig
	planner   *StrategyPlanner
	bus       *securebus.Bus
	callModel CallModelFunc
}

// NewEngine creates an RLMEngine. bus may be nil in tests (only FanOut logic
// is exercised). callModel is required.
func NewEngine(cfg EngineConfig, bus *securebus.Bus, callModel CallModelFunc) *Engine {
	return &Engine{
		cfg:       cfg,
		planner:   NewStrategyPlanner(cfg.Strategy),
		bus:       bus,
		callModel: callModel,
	}
}

// Answer is the entry point for an RLM query. It recursively decomposes the
// context until each partition fits within the direct threshold, then synthesises
// the sub-answers bottom-up.
//
// sessionKey is passed through to the SecureBus for audit tracing.
func (e *Engine) Answer(ctx context.Context, sessionKey, query, context_ string) (string, uint32, error) {
	return e.recurse(ctx, sessionKey, query, context_, 0)
}

// recurse is the recursive heart of the engine.
func (e *Engine) recurse(ctx context.Context, sessionKey, query, ctxContent string, depth uint8) (string, uint32, error) {
	rope := NewRope(ctxContent)
	op := e.planner.PlanNext(rope.Len(), query, depth)

	switch op.Type {
	case OpFinal:
		return e.callModel(ctx, ctxContent, query)

	case OpGrep:
		matches := rope.GrepLines(op.GrepQuery, 50, true)
		if len(matches) == 0 {
			// No matches — fall back to direct answer.
			return e.callModel(ctx, ctxContent, query)
		}
		var sb strings.Builder
		for _, m := range matches {
			sb.WriteString(m.Line)
			sb.WriteByte('\n')
		}
		return e.recurse(ctx, sessionKey, query, sb.String(), depth+1)

	case OpPartition:
		k := op.PartitionK
		if k <= 0 {
			k = e.cfg.Strategy.DefaultPartitionK
		}
		partitions := rope.Partition(k)

		// Fan out: process each partition concurrently.
		results := FanOut(ctx, partitions, e.cfg.MaxConcurrency,
			func(ctx context.Context, idx int, contextKey, partition string) PartitionResult {
				if e.bus != nil {
					// Route through SecureBus for audit logging.
					req := itr.NewRecurseRequest(
						fmt.Sprintf("%s-d%d-p%d", sessionKey, depth, idx),
						sessionKey,
						depth+1,
						query,
						contextKey,
						depth+1,
					)
					resp := e.bus.Execute(ctx, req)
					if resp.IsError {
						return PartitionResult{
							PartitionIdx: idx,
							ContextKey:   contextKey,
							Err:          fmt.Errorf("securebus: %s", resp.Result),
						}
					}
					// The bus returned a stub response for RLM commands —
					// recurse directly to get the actual answer.
				}
				answer, tokens, err := e.recurse(ctx, sessionKey, query, partition, depth+1)
				return PartitionResult{
					PartitionIdx: idx,
					ContextKey:   contextKey,
					Answer:       answer,
					Tokens:       tokens,
					Err:          err,
				}
			})

		merged := MergeResults(results)
		totalTok := TotalTokens(results)

		// If we got something useful, do a final synthesis pass to produce
		// a coherent answer from the merged sub-answers.
		if merged != "" && int64(depth) < int64(e.cfg.Strategy.MaxDepth)-1 {
			synth, syntTok, err := e.callModel(ctx,
				"You are synthesising answers from multiple text partitions. Be concise.",
				"Original question: "+query+"\n\nPartition answers:\n"+merged)
			return synth, totalTok + syntTok, err
		}
		return merged, totalTok, nil

	default:
		return "", 0, fmt.Errorf("rlm: unknown op %q", op.Type)
	}
}
