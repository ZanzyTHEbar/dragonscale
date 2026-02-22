package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

type retrievalMode string

const (
	retrievalModeShadow   retrievalMode = "shadow"
	retrievalModePromoted retrievalMode = "promoted"
	retrievalModeRollback retrievalMode = "rollback"
)

const (
	retrievalPolicyStateKey   = "memory:retrieval:policy_state"
	retrievalPolicyGatesKey   = "memory:retrieval:promotion_gates"
	retrievalPolicyMetricsKey = "memory:retrieval:shadow_metrics"
)

type retrievalPolicyState struct {
	Mode      retrievalMode `json:"mode"`
	Reason    string        `json:"reason,omitempty"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type retrievalPromotionGates struct {
	MinSamples           int     `json:"min_samples"`
	MinAugmentedSamples  int     `json:"min_augmented_samples"`
	MinTop1Parity        float64 `json:"min_top1_parity"`
	MinOverlapAtK        float64 `json:"min_overlap_at_k"`
	MinPromotedSamples   int     `json:"min_promoted_samples"`
	RollbackTop1Parity   float64 `json:"rollback_top1_parity"`
	RollbackOverlapAtK   float64 `json:"rollback_overlap_at_k"`
	MaxNoResultRate      float64 `json:"max_no_result_rate"`
	PromotedNoResultRate float64 `json:"promoted_no_result_rate"`
}

type retrievalShadowMetrics struct {
	TotalQueries            int       `json:"total_queries"`
	NoResultQueries         int       `json:"no_result_queries"`
	AugmentedQueries        int       `json:"augmented_queries"`
	Top1ParityHits          int       `json:"top1_parity_hits"`
	OverlapAtKTotal         float64   `json:"overlap_at_k_total"`
	PromotedQueries         int       `json:"promoted_queries"`
	PromotedTop1Hits        int       `json:"promoted_top1_hits"`
	PromotedOverlap         float64   `json:"promoted_overlap_total"`
	PromotedNoResultQueries int       `json:"promoted_no_result_queries"`
	UpdatedAt               time.Time `json:"updated_at"`
}

type retrievalParity struct {
	Top1Match    bool
	OverlapAtK   float64
	NoResultPair bool
}

func defaultRetrievalPolicyState() retrievalPolicyState {
	return retrievalPolicyState{
		Mode:      retrievalModeShadow,
		Reason:    "default_shadow_bootstrap",
		UpdatedAt: time.Now().UTC(),
	}
}

func defaultRetrievalPromotionGates() retrievalPromotionGates {
	return retrievalPromotionGates{
		MinSamples:           25,
		MinAugmentedSamples:  10,
		MinTop1Parity:        0.65,
		MinOverlapAtK:        0.60,
		MinPromotedSamples:   10,
		RollbackTop1Parity:   0.45,
		RollbackOverlapAtK:   0.35,
		MaxNoResultRate:      0.90,
		PromotedNoResultRate: 0.95,
	}
}

func defaultRetrievalShadowMetrics() retrievalShadowMetrics {
	return retrievalShadowMetrics{
		UpdatedAt: time.Now().UTC(),
	}
}

func (m *MemoryStore) loadRetrievalPolicyState(ctx context.Context) retrievalPolicyState {
	raw, err := m.delegate.GetKV(ctx, m.agentID, retrievalPolicyStateKey)
	if err != nil || raw == "" {
		state := defaultRetrievalPolicyState()
		if perr := m.persistRetrievalPolicyState(ctx, state); perr != nil {
			logger.WarnCF("memory", "failed to persist default retrieval policy state",
				map[string]interface{}{"error": perr.Error()})
		}
		return state
	}

	var state retrievalPolicyState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		state = defaultRetrievalPolicyState()
		if perr := m.persistRetrievalPolicyState(ctx, state); perr != nil {
			logger.WarnCF("memory", "failed to persist repaired retrieval policy state",
				map[string]interface{}{"error": perr.Error()})
		}
		return state
	}
	if !isValidRetrievalMode(state.Mode) {
		state = defaultRetrievalPolicyState()
		if perr := m.persistRetrievalPolicyState(ctx, state); perr != nil {
			logger.WarnCF("memory", "failed to persist healed retrieval policy state",
				map[string]interface{}{"error": perr.Error()})
		}
	}
	return state
}

func (m *MemoryStore) loadRetrievalPromotionGates(ctx context.Context) retrievalPromotionGates {
	raw, err := m.delegate.GetKV(ctx, m.agentID, retrievalPolicyGatesKey)
	if err != nil || raw == "" {
		gates := defaultRetrievalPromotionGates()
		if perr := m.persistRetrievalPromotionGates(ctx, gates); perr != nil {
			logger.WarnCF("memory", "failed to persist default retrieval promotion gates",
				map[string]interface{}{"error": perr.Error()})
		}
		return gates
	}

	var gates retrievalPromotionGates
	if err := json.Unmarshal([]byte(raw), &gates); err != nil {
		gates = defaultRetrievalPromotionGates()
		if perr := m.persistRetrievalPromotionGates(ctx, gates); perr != nil {
			logger.WarnCF("memory", "failed to persist repaired retrieval promotion gates",
				map[string]interface{}{"error": perr.Error()})
		}
		return gates
	}

	// Guard-clauses for malformed/partial gate records.
	if gates.MinSamples <= 0 {
		gates.MinSamples = defaultRetrievalPromotionGates().MinSamples
	}
	if gates.MinPromotedSamples <= 0 {
		gates.MinPromotedSamples = defaultRetrievalPromotionGates().MinPromotedSamples
	}
	if gates.MinAugmentedSamples <= 0 {
		gates.MinAugmentedSamples = defaultRetrievalPromotionGates().MinAugmentedSamples
	}
	if gates.MinTop1Parity <= 0 {
		gates.MinTop1Parity = defaultRetrievalPromotionGates().MinTop1Parity
	}
	if gates.MinOverlapAtK <= 0 {
		gates.MinOverlapAtK = defaultRetrievalPromotionGates().MinOverlapAtK
	}
	if gates.RollbackTop1Parity <= 0 {
		gates.RollbackTop1Parity = defaultRetrievalPromotionGates().RollbackTop1Parity
	}
	if gates.RollbackOverlapAtK <= 0 {
		gates.RollbackOverlapAtK = defaultRetrievalPromotionGates().RollbackOverlapAtK
	}
	if gates.MaxNoResultRate <= 0 {
		gates.MaxNoResultRate = defaultRetrievalPromotionGates().MaxNoResultRate
	}
	if gates.PromotedNoResultRate <= 0 {
		gates.PromotedNoResultRate = defaultRetrievalPromotionGates().PromotedNoResultRate
	}
	return gates
}

func (m *MemoryStore) loadRetrievalShadowMetrics(ctx context.Context) retrievalShadowMetrics {
	raw, err := m.delegate.GetKV(ctx, m.agentID, retrievalPolicyMetricsKey)
	if err != nil || raw == "" {
		metrics := defaultRetrievalShadowMetrics()
		if perr := m.persistRetrievalShadowMetrics(ctx, metrics); perr != nil {
			logger.WarnCF("memory", "failed to persist default retrieval shadow metrics",
				map[string]interface{}{"error": perr.Error()})
		}
		return metrics
	}

	var metrics retrievalShadowMetrics
	if err := json.Unmarshal([]byte(raw), &metrics); err != nil {
		metrics = defaultRetrievalShadowMetrics()
		if perr := m.persistRetrievalShadowMetrics(ctx, metrics); perr != nil {
			logger.WarnCF("memory", "failed to persist repaired retrieval shadow metrics",
				map[string]interface{}{"error": perr.Error()})
		}
		return metrics
	}
	return metrics
}

func (m *MemoryStore) persistRetrievalPolicyState(ctx context.Context, state retrievalPolicyState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal retrieval policy state: %w", err)
	}
	if err := m.delegate.UpsertKV(ctx, m.agentID, retrievalPolicyStateKey, string(payload)); err != nil {
		return fmt.Errorf("upsert retrieval policy state: %w", err)
	}
	return nil
}

func (m *MemoryStore) persistRetrievalPromotionGates(ctx context.Context, gates retrievalPromotionGates) error {
	payload, err := json.Marshal(gates)
	if err != nil {
		return fmt.Errorf("marshal retrieval promotion gates: %w", err)
	}
	if err := m.delegate.UpsertKV(ctx, m.agentID, retrievalPolicyGatesKey, string(payload)); err != nil {
		return fmt.Errorf("upsert retrieval promotion gates: %w", err)
	}
	return nil
}

func (m *MemoryStore) persistRetrievalShadowMetrics(ctx context.Context, metrics retrievalShadowMetrics) error {
	payload, err := json.Marshal(metrics)
	if err != nil {
		return fmt.Errorf("marshal retrieval shadow metrics: %w", err)
	}
	if err := m.delegate.UpsertKV(ctx, m.agentID, retrievalPolicyMetricsKey, string(payload)); err != nil {
		return fmt.Errorf("upsert retrieval shadow metrics: %w", err)
	}
	return nil
}

func computeRetrievalParity(baseline, augmented []memory.SearchResult, k int) retrievalParity {
	if k <= 0 {
		k = 5
	}
	parity := retrievalParity{
		NoResultPair: len(baseline) == 0 && len(augmented) == 0,
	}
	if len(baseline) > 0 && len(augmented) > 0 {
		parity.Top1Match = baseline[0].ID == augmented[0].ID
	}

	baseTop := topKResultIDs(baseline, k)
	augTop := topKResultIDs(augmented, k)
	if len(baseTop) == 0 && len(augTop) == 0 {
		parity.OverlapAtK = 1.0
		return parity
	}

	union := make(map[ids.UUID]bool, len(baseTop)+len(augTop))
	intersection := 0
	for id := range baseTop {
		union[id] = true
		if augTop[id] {
			intersection++
		}
	}
	for id := range augTop {
		union[id] = true
	}
	if len(union) > 0 {
		parity.OverlapAtK = float64(intersection) / float64(len(union))
	}
	return parity
}

func topKResultIDs(results []memory.SearchResult, k int) map[ids.UUID]bool {
	if k <= 0 {
		return nil
	}
	if len(results) < k {
		k = len(results)
	}
	out := make(map[ids.UUID]bool, k)
	for i := 0; i < k; i++ {
		out[results[i].ID] = true
	}
	return out
}

func (m *MemoryStore) updateRetrievalPolicy(
	ctx context.Context,
	state retrievalPolicyState,
	gates retrievalPromotionGates,
	metrics retrievalShadowMetrics,
	parity retrievalParity,
	augmentedUsed bool,
	baseline []memory.SearchResult,
) (retrievalPolicyState, retrievalShadowMetrics) {
	prevMode := state.Mode

	metrics.TotalQueries++
	if len(baseline) == 0 {
		metrics.NoResultQueries++
	}
	if augmentedUsed {
		metrics.AugmentedQueries++
	}
	if parity.Top1Match {
		metrics.Top1ParityHits++
	}
	metrics.OverlapAtKTotal += parity.OverlapAtK
	metrics.UpdatedAt = time.Now().UTC()

	switch state.Mode {
	case retrievalModeShadow:
		top1Rate := safeRate(metrics.Top1ParityHits, metrics.TotalQueries)
		overlapAvg := safeAvg(metrics.OverlapAtKTotal, metrics.TotalQueries)
		noResultRate := safeRate(metrics.NoResultQueries, metrics.TotalQueries)

		if metrics.TotalQueries >= gates.MinSamples &&
			metrics.AugmentedQueries >= gates.MinAugmentedSamples &&
			top1Rate >= gates.MinTop1Parity &&
			overlapAvg >= gates.MinOverlapAtK &&
			noResultRate <= gates.MaxNoResultRate {
			state.Mode = retrievalModePromoted
			state.Reason = fmt.Sprintf(
				"promotion_gates_passed(top1=%.3f overlap=%.3f samples=%d)",
				top1Rate,
				overlapAvg,
				metrics.TotalQueries,
			)
			state.UpdatedAt = time.Now().UTC()
			metrics.PromotedQueries = 0
			metrics.PromotedTop1Hits = 0
			metrics.PromotedOverlap = 0
			metrics.PromotedNoResultQueries = 0
		}

	case retrievalModePromoted:
		metrics.PromotedQueries++
		if len(baseline) == 0 {
			metrics.PromotedNoResultQueries++
		}
		if parity.Top1Match {
			metrics.PromotedTop1Hits++
		}
		metrics.PromotedOverlap += parity.OverlapAtK

		if metrics.PromotedQueries >= gates.MinPromotedSamples {
			promotedTop1 := safeRate(metrics.PromotedTop1Hits, metrics.PromotedQueries)
			promotedOverlap := safeAvg(metrics.PromotedOverlap, metrics.PromotedQueries)
			promotedNoResultRate := safeRate(metrics.PromotedNoResultQueries, metrics.PromotedQueries)

			if promotedTop1 < gates.RollbackTop1Parity ||
				promotedOverlap < gates.RollbackOverlapAtK ||
				promotedNoResultRate > gates.PromotedNoResultRate {
				state.Mode = retrievalModeRollback
				state.Reason = fmt.Sprintf(
					"rollback_triggered(top1=%.3f overlap=%.3f no_result_rate=%.3f promoted_samples=%d)",
					promotedTop1,
					promotedOverlap,
					promotedNoResultRate,
					metrics.PromotedQueries,
				)
				state.UpdatedAt = time.Now().UTC()
			}
		}
	}

	if prevMode != state.Mode {
		logger.InfoCF("memory", "retrieval policy mode transition",
			map[string]interface{}{
				"from":              prevMode,
				"to":                state.Mode,
				"reason":            state.Reason,
				"total_queries":     metrics.TotalQueries,
				"augmented_queries": metrics.AugmentedQueries,
				"promoted_queries":  metrics.PromotedQueries,
			})
	}

	// State and metrics are written back to the in-memory policy cache by the
	// caller; the cache handles periodic flush to KV. On mode transitions we
	// flush immediately to guarantee durability.
	if prevMode != state.Mode {
		if err := m.persistRetrievalShadowMetrics(ctx, metrics); err != nil {
			logger.WarnCF("memory", "failed to persist retrieval shadow metrics",
				map[string]interface{}{"mode": state.Mode, "error": err.Error()})
		}
		if err := m.persistRetrievalPolicyState(ctx, state); err != nil {
			logger.WarnCF("memory", "failed to persist retrieval policy state",
				map[string]interface{}{"mode": state.Mode, "error": err.Error()})
		}
	}
	return state, metrics
}

func safeRate(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func safeAvg(total float64, count int) float64 {
	if count <= 0 {
		return 0
	}
	return total / float64(count)
}

func isValidRetrievalMode(mode retrievalMode) bool {
	switch mode {
	case retrievalModeShadow, retrievalModePromoted, retrievalModeRollback:
		return true
	default:
		return false
	}
}
