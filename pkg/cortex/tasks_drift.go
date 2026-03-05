package cortex

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/ids"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/memory"
)

// DriftStatus represents the health state of a domain.
type DriftStatus string

const (
	StatusActive     DriftStatus = "active"     // Normal activity
	StatusDrifting   DriftStatus = "drifting"   // Activity declining
	StatusNeglected  DriftStatus = "neglected"  // No recent activity
	StatusCold       DriftStatus = "cold"       // Long-term inactivity
	StatusOveractive DriftStatus = "overactive" // Too much activity (possible loop)
)

// DomainDrift tracks health metrics for a knowledge domain.
type DomainDrift struct {
	DomainID     string      `json:"domain_id"`
	AgentID      string      `json:"agent_id"`
	Status       DriftStatus `json:"status"`
	Score        float64     `json:"score"` // 0.0-1.0 health score
	LastActivity time.Time   `json:"last_activity"`

	// Activity metrics
	MessageCount  int     `json:"message_count"`   // Messages in period
	ToolCallCount int     `json:"tool_call_count"` // Tool calls in period
	MemoryCount   int     `json:"memory_count"`    // Memories created
	AvgImportance float64 `json:"avg_importance"`  // Average memory importance

	// Trending
	TrendDirection string  `json:"trend_direction"` // "up", "down", "stable"
	TrendMagnitude float64 `json:"trend_magnitude"` // Rate of change

	// Analysis
	DetectedAt     time.Time `json:"detected_at"`
	Recommendation string    `json:"recommendation"`
}

// DriftStore provides access to domain and activity data.
type DriftStore interface {
	// GetDomains retrieves all tracked domains for an agent
	GetDomains(ctx context.Context, agentID string) ([]string, error)

	// GetDomainActivity retrieves activity metrics for a domain
	GetDomainActivity(ctx context.Context, agentID string, domain string, since time.Time) (*DomainActivity, error)

	// GetHistoricalMetrics retrieves past metrics for trend analysis
	GetHistoricalMetrics(ctx context.Context, agentID string, domain string, periods int) ([]*DomainMetrics, error)

	// StoreDriftStatus saves the current drift status
	StoreDriftStatus(ctx context.Context, drift *DomainDrift) error

	// GetDriftStatus retrieves the current drift status for a domain
	GetDriftStatus(ctx context.Context, agentID string, domain string) (*DomainDrift, error)

	// ListActiveAgents returns all agent IDs with recent activity
	ListActiveAgents(ctx context.Context, since time.Time) ([]string, error)
}

// DomainActivity holds raw activity counts.
type DomainActivity struct {
	Domain        string
	MessageCount  int
	ToolCallCount int
	MemoryCount   int
	AvgImportance float64
	LastActivity  time.Time
}

// DomainMetrics holds aggregated metrics for a time period.
type DomainMetrics struct {
	Period        time.Time
	Score         float64
	MessageCount  int
	ToolCallCount int
	MemoryCount   int
}

// DriftConfig configures the drift detection task.
type DriftConfig struct {
	CheckInterval       time.Duration // How often to check (default 10 min)
	ActivityWindow      time.Duration // Window for activity analysis (default 1h)
	TrendPeriods        int           // Number of periods for trend (default 6)
	DriftThreshold      float64       // Score below this triggers drift alert (default 0.3)
	NeglectThreshold    float64       // Score below this triggers neglect (default 0.1)
	OveractiveThreshold float64       // Score above this triggers overactive (default 0.9)
	Timeout             time.Duration // Max execution time (default 60s)
}

// DefaultDriftConfig returns sensible defaults.
func DefaultDriftConfig() DriftConfig {
	return DriftConfig{
		CheckInterval:       10 * time.Minute,
		ActivityWindow:      1 * time.Hour,
		TrendPeriods:        6,
		DriftThreshold:      0.3,
		NeglectThreshold:    0.1,
		OveractiveThreshold: 0.9,
		Timeout:             60 * time.Second,
	}
}

// DriftTask detects domain drift and health degradation.
type DriftTask struct {
	cfg   DriftConfig
	store DriftStore
}

// NewDriftTask creates a drift detection task.
func NewDriftTask(cfg DriftConfig, store DriftStore) *DriftTask {
	return &DriftTask{
		cfg:   cfg,
		store: store,
	}
}

// Name returns the task identifier.
func (t *DriftTask) Name() string {
	return "drift"
}

// Interval returns the task run interval.
func (t *DriftTask) Interval() time.Duration {
	return t.cfg.CheckInterval
}

// Timeout returns the maximum execution time for one drift cycle.
func (t *DriftTask) Timeout() time.Duration {
	return t.cfg.Timeout
}

// Execute performs drift detection across all agents and their domains.
func (t *DriftTask) Execute(ctx context.Context) error {
	logger.InfoCF("cortex", "Running drift detection", map[string]interface{}{"task": "drift"})

	// Get all active agents
	since := time.Now().Add(-t.cfg.ActivityWindow)
	agents, err := t.store.ListActiveAgents(ctx, since)
	if err != nil {
		logger.WarnCF("cortex", "Failed to list active agents; skipping drift cycle",
			map[string]interface{}{"error": err.Error()})
		return nil
	}
	if len(agents) == 0 {
		logger.DebugCF("cortex", "No active agents found, skipping drift detection", nil)
		return nil
	}

	// Process drift detection for each agent
	var totalDriftDetected int
	for _, agentID := range agents {
		count, err := t.processAgentDrift(ctx, agentID)
		if err != nil {
			logger.WarnCF("cortex", "Failed to process drift for agent",
				map[string]interface{}{"agent_id": agentID, "error": err.Error()})
			// Continue with other agents
		}
		totalDriftDetected += count
	}

	logger.DebugCF("cortex", "Drift detection complete", map[string]interface{}{
		"task":           "drift",
		"agents_checked": len(agents),
		"drift_detected": totalDriftDetected,
	})

	return nil
}

// processAgentDrift performs drift detection for a single agent.
func (t *DriftTask) processAgentDrift(ctx context.Context, agentID string) (int, error) {
	// Get all domains for this agent
	domains, err := t.store.GetDomains(ctx, agentID)
	if err != nil {
		return 0, fmt.Errorf("get domains for agent %s: %w", agentID, err)
	}

	logger.DebugCF("cortex", "Checking drift for domains", map[string]interface{}{
		"task":         "drift",
		"agent_id":     agentID,
		"domain_count": len(domains),
	})

	var driftDetected int
	for _, domain := range domains {
		drift, err := t.analyzeDomain(ctx, agentID, domain)
		if err != nil {
			logger.DebugCF("cortex", "Failed to analyze domain", map[string]interface{}{
				"error":    err,
				"agent_id": agentID,
				"domain":   domain,
			})
			continue
		}

		if err := t.store.StoreDriftStatus(ctx, drift); err != nil {
			logger.DebugCF("cortex", "Failed to store drift status", map[string]interface{}{
				"error":    err,
				"agent_id": agentID,
				"domain":   domain,
			})
			continue
		}

		if drift.Status != StatusActive {
			driftDetected++
			logger.DebugCF("cortex", "Drift detected", map[string]interface{}{
				"task":           "drift",
				"domain":         domain,
				"status":         drift.Status,
				"score":          fmt.Sprintf("%.2f", drift.Score),
				"recommendation": drift.Recommendation,
			})
		}
	}

	logger.DebugCF("cortex", "Drift detection complete for agent", map[string]interface{}{
		"task":            "drift",
		"agent_id":        agentID,
		"domains_checked": len(domains),
		"drift_detected":  driftDetected,
	})

	return driftDetected, nil
}

func (t *DriftTask) analyzeDomain(ctx context.Context, agentID, domain string) (*DomainDrift, error) {
	since := time.Now().Add(-t.cfg.ActivityWindow)

	// Get current activity
	activity, err := t.store.GetDomainActivity(ctx, agentID, domain, since)
	if err != nil {
		return nil, fmt.Errorf("get domain activity: %w", err)
	}

	// Get historical metrics for trend analysis
	history, err := t.store.GetHistoricalMetrics(ctx, agentID, domain, t.cfg.TrendPeriods)
	if err != nil {
		// Continue without trend analysis
		history = nil
	}

	// Calculate health score
	score := t.calculateHealthScore(activity, history)

	// Determine status based on score and activity patterns
	status := t.determineStatus(score, activity)

	// Calculate trend
	trendDirection, trendMagnitude := t.calculateTrend(history)

	// Generate recommendation
	recommendation := t.generateRecommendation(status, score, activity, trendDirection)

	drift := &DomainDrift{
		DomainID:       domain,
		AgentID:        agentID,
		Status:         status,
		Score:          score,
		LastActivity:   activity.LastActivity,
		MessageCount:   activity.MessageCount,
		ToolCallCount:  activity.ToolCallCount,
		MemoryCount:    activity.MemoryCount,
		AvgImportance:  activity.AvgImportance,
		TrendDirection: trendDirection,
		TrendMagnitude: trendMagnitude,
		DetectedAt:     time.Now(),
		Recommendation: recommendation,
	}

	return drift, nil
}

func (t *DriftTask) calculateHealthScore(activity *DomainActivity, history []*DomainMetrics) float64 {
	// Base score from activity levels
	score := 0.5

	// Factor in message volume (normalized to healthy range of 5-20 per hour)
	msgScore := float64(activity.MessageCount) / 10.0
	if msgScore > 1.0 {
		msgScore = 1.0 // Cap at 1.0
	}

	// Factor in memory creation (should have some memory creation)
	memScore := math.Min(float64(activity.MemoryCount)/3.0, 1.0)

	// Factor in importance (higher importance = more engaged)
	impScore := activity.AvgImportance

	// Factor in tool usage (indicates action)
	toolScore := math.Min(float64(activity.ToolCallCount)/5.0, 1.0)

	// Weighted combination
	score = 0.3*msgScore + 0.2*memScore + 0.3*impScore + 0.2*toolScore

	// Apply trend adjustment
	if len(history) >= 2 {
		recent := history[len(history)-1].Score
		older := history[0].Score
		trend := recent - older

		// Declining trend reduces score
		if trend < -0.1 {
			score -= 0.1
		}
		// Improving trend increases score
		if trend > 0.1 {
			score += 0.1
		}
	}

	return math.Max(0.0, math.Min(1.0, score))
}

func (t *DriftTask) determineStatus(score float64, activity *DomainActivity) DriftStatus {
	// Check for overactivity (possible loops or spam)
	if score > t.cfg.OveractiveThreshold && activity.MessageCount > 50 {
		return StatusOveractive
	}

	// Check for neglect (no recent activity)
	if score < t.cfg.NeglectThreshold {
		return StatusNeglected
	}

	// Check for drifting (declining activity)
	if score < t.cfg.DriftThreshold {
		return StatusDrifting
	}

	return StatusActive
}

func (t *DriftTask) calculateTrend(history []*DomainMetrics) (string, float64) {
	if len(history) < 2 {
		return "unknown", 0.0
	}

	// Simple linear regression on scores
	n := float64(len(history))
	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0

	for i, m := range history {
		x := float64(i)
		y := m.Score
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	// Slope of regression line
	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)

	// Determine direction
	direction := "stable"
	if slope > 0.05 {
		direction = "up"
	} else if slope < -0.05 {
		direction = "down"
	}

	return direction, slope
}

func (t *DriftTask) generateRecommendation(status DriftStatus, score float64, activity *DomainActivity, trend string) string {
	switch status {
	case StatusActive:
		if trend == "up" {
			return "Domain is healthy and growing. Continue current engagement."
		}
		return "Domain is healthy. Maintain current activity levels."

	case StatusDrifting:
		if activity.MessageCount < 5 {
			return "Low engagement detected. Consider prompting user for updates."
		}
		if activity.AvgImportance < 0.3 {
			return "Activity quality declining. Review memory importance settings."
		}
		return "Activity declining. Surface relevant context to re-engage."

	case StatusNeglected:
		return "Domain neglected. Archive old memories or prompt for status update."

	case StatusCold:
		return "Domain inactive for extended period. Consider archiving."

	case StatusOveractive:
		return "Possible loop or spam detected. Review agent behavior patterns."

	default:
		return "Monitor domain activity."
	}
}

// GetDomainHealth returns a summary of domain health across all domains.
func GetDomainHealth(store DriftStore, agentID string) (*HealthSummary, error) {
	ctx := context.Background()

	domains, err := store.GetDomains(ctx, agentID)
	if err != nil {
		return nil, err
	}

	summary := &HealthSummary{
		TotalDomains:    len(domains),
		DomainBreakdown: make(map[DriftStatus]int),
	}

	var totalScore float64
	for _, domain := range domains {
		drift, err := store.GetDriftStatus(ctx, agentID, domain)
		if err != nil {
			continue
		}

		summary.DomainBreakdown[drift.Status]++
		totalScore += drift.Score

		if drift.Status != StatusActive {
			summary.UnhealthyDomains = append(summary.UnhealthyDomains, drift)
		}
	}

	if len(domains) > 0 {
		summary.AverageScore = totalScore / float64(len(domains))
	}

	return summary, nil
}

// HealthSummary provides an overview of domain health.
type HealthSummary struct {
	TotalDomains     int
	AverageScore     float64
	DomainBreakdown  map[DriftStatus]int
	UnhealthyDomains []*DomainDrift
}

// Format returns a human-readable summary.
func (hs *HealthSummary) Format() string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Domain Health Summary: %.0f%% average score\n", hs.AverageScore*100))
	b.WriteString(fmt.Sprintf("Total domains: %d\n", hs.TotalDomains))

	for status, count := range hs.DomainBreakdown {
		if count > 0 {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", status, count))
		}
	}

	if len(hs.UnhealthyDomains) > 0 {
		b.WriteString("\nUnhealthy domains:\n")
		for _, d := range hs.UnhealthyDomains {
			b.WriteString(fmt.Sprintf("  - %s: %s (score: %.2f)\n", d.DomainID, d.Status, d.Score))
		}
	}

	return b.String()
}

const (
	driftSessionKey = "__cortex_drift__"
	driftTagPrefix  = "drift_status"
	driftPageSize   = 500
	driftMaxScan    = 20000
)

// DriftMemorySource is the minimal memory interface needed by MemoryDriftAdapter.
type DriftMemorySource interface {
	ListActiveAgents(ctx context.Context, since time.Time) ([]string, error)
	ListRecallItems(ctx context.Context, agentID, sessionKey string, limit, offset int) ([]*memory.RecallItem, error)
	InsertRecallItem(ctx context.Context, item *memory.RecallItem) error
}

// MemoryDriftAdapter adapts memory delegate data into DriftStore metrics.
type MemoryDriftAdapter struct {
	source DriftMemorySource
}

// Ensure MemoryDriftAdapter implements DriftStore.
var _ DriftStore = (*MemoryDriftAdapter)(nil)

// NewMemoryDriftAdapter creates a drift adapter backed by memory recall data.
func NewMemoryDriftAdapter(source DriftMemorySource) *MemoryDriftAdapter {
	return &MemoryDriftAdapter{source: source}
}

func (m *MemoryDriftAdapter) GetDomains(ctx context.Context, agentID string) ([]string, error) {
	if m == nil || m.source == nil {
		return []string{}, nil
	}

	items, err := m.listRecallItemsAll(ctx, agentID, "")
	if err != nil {
		return nil, fmt.Errorf("list recall items for domains: %w", err)
	}

	seen := map[string]struct{}{}
	for _, item := range items {
		if isDriftSyntheticItem(item) {
			continue
		}
		domain := strings.TrimSpace(string(item.Sector))
		if domain == "" {
			continue
		}
		seen[domain] = struct{}{}
	}

	if len(seen) == 0 {
		return []string{"general"}, nil
	}

	domains := make([]string, 0, len(seen))
	for domain := range seen {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains, nil
}

func (m *MemoryDriftAdapter) GetDomainActivity(ctx context.Context, agentID, domain string, since time.Time) (*DomainActivity, error) {
	activity := &DomainActivity{
		Domain:       domain,
		LastActivity: since,
	}
	if m == nil || m.source == nil {
		return activity, nil
	}

	items, err := m.listRecallItemsAll(ctx, agentID, "")
	if err != nil {
		return nil, fmt.Errorf("list recall items for domain activity: %w", err)
	}

	var importanceTotal float64
	for _, item := range items {
		if isDriftSyntheticItem(item) {
			continue
		}
		if strings.TrimSpace(string(item.Sector)) != domain {
			continue
		}

		ts := item.UpdatedAt
		if ts.IsZero() {
			ts = item.CreatedAt
		}
		if !since.IsZero() && ts.Before(since) {
			continue
		}

		activity.MessageCount++
		activity.MemoryCount++
		importanceTotal += item.Importance
		if item.Role == "tool" || strings.Contains(item.Tags, "tool") {
			activity.ToolCallCount++
		}
		if ts.After(activity.LastActivity) {
			activity.LastActivity = ts
		}
	}

	if activity.MemoryCount > 0 {
		activity.AvgImportance = importanceTotal / float64(activity.MemoryCount)
	}
	return activity, nil
}

func (m *MemoryDriftAdapter) GetHistoricalMetrics(ctx context.Context, agentID string, domain string, periods int) ([]*DomainMetrics, error) {
	if m == nil || m.source == nil || periods <= 0 {
		return nil, nil
	}

	items, err := m.listRecallItemsAll(ctx, agentID, driftSessionKey)
	if err != nil {
		return nil, fmt.Errorf("list drift history: %w", err)
	}

	metrics := make([]*DomainMetrics, 0, periods)
	for _, item := range items {
		if !hasDriftDomainTag(item.Tags, domain) {
			continue
		}

		var drift DomainDrift
		if err := json.Unmarshal([]byte(item.Content), &drift); err != nil {
			continue
		}

		metrics = append(metrics, &DomainMetrics{
			Period:        item.CreatedAt,
			Score:         drift.Score,
			MessageCount:  drift.MessageCount,
			ToolCallCount: drift.ToolCallCount,
			MemoryCount:   drift.MemoryCount,
		})
	}

	if len(metrics) == 0 {
		return nil, nil
	}

	sort.Slice(metrics, func(i, j int) bool {
		return metrics[i].Period.Before(metrics[j].Period)
	})
	if len(metrics) > periods {
		metrics = metrics[len(metrics)-periods:]
	}
	return metrics, nil
}

func (m *MemoryDriftAdapter) StoreDriftStatus(ctx context.Context, drift *DomainDrift) error {
	if drift == nil || m == nil || m.source == nil {
		return nil
	}

	payload, err := json.Marshal(drift)
	if err != nil {
		return fmt.Errorf("marshal drift status: %w", err)
	}

	observedAt := drift.DetectedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	item := &memory.RecallItem{
		ID:         ids.New(),
		AgentID:    drift.AgentID,
		SessionKey: driftSessionKey,
		Role:       "system",
		Sector:     memory.SectorReflective,
		Importance: 0.6,
		Salience:   0.6,
		Content:    string(payload),
		Tags:       fmt.Sprintf("%s,domain:%s,status:%s", driftTagPrefix, drift.DomainID, drift.Status),
		CreatedAt:  observedAt,
		UpdatedAt:  observedAt,
	}
	return m.source.InsertRecallItem(ctx, item)
}

func (m *MemoryDriftAdapter) GetDriftStatus(ctx context.Context, agentID string, domain string) (*DomainDrift, error) {
	defaultStatus := &DomainDrift{
		DomainID:   domain,
		AgentID:    agentID,
		Status:     StatusActive,
		Score:      0.5,
		DetectedAt: time.Now(),
	}
	if m == nil || m.source == nil {
		return defaultStatus, nil
	}

	items, err := m.listRecallItemsAll(ctx, agentID, driftSessionKey)
	if err != nil {
		return nil, fmt.Errorf("list drift statuses: %w", err)
	}

	for _, item := range items {
		if !hasDriftDomainTag(item.Tags, domain) {
			continue
		}
		var drift DomainDrift
		if err := json.Unmarshal([]byte(item.Content), &drift); err != nil {
			continue
		}
		if drift.DomainID == "" {
			drift.DomainID = domain
		}
		if drift.AgentID == "" {
			drift.AgentID = agentID
		}
		return &drift, nil
	}

	return defaultStatus, nil
}

func (m *MemoryDriftAdapter) ListActiveAgents(ctx context.Context, since time.Time) ([]string, error) {
	if m == nil || m.source == nil {
		return []string{}, nil
	}
	return m.source.ListActiveAgents(ctx, since)
}

func hasDriftDomainTag(tags, domain string) bool {
	if tags == "" || domain == "" {
		return false
	}
	parts := strings.Split(tags, ",")
	want := "domain:" + domain
	hasPrefix := false
	hasDomain := false
	for _, raw := range parts {
		tag := strings.TrimSpace(raw)
		if tag == driftTagPrefix {
			hasPrefix = true
		}
		if tag == want {
			hasDomain = true
		}
	}
	return hasPrefix && hasDomain
}

func (m *MemoryDriftAdapter) listRecallItemsAll(ctx context.Context, agentID, sessionKey string) ([]*memory.RecallItem, error) {
	if m == nil || m.source == nil {
		return nil, nil
	}

	all := make([]*memory.RecallItem, 0, driftPageSize)
	offset := 0
	for offset < driftMaxScan {
		page, err := m.source.ListRecallItems(ctx, agentID, sessionKey, driftPageSize, offset)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		if len(page) < driftPageSize {
			break
		}
		offset += len(page)
	}
	return all, nil
}

func isDriftSyntheticItem(item *memory.RecallItem) bool {
	if item == nil {
		return false
	}
	if item.SessionKey == driftSessionKey {
		return true
	}
	for _, raw := range strings.Split(item.Tags, ",") {
		if strings.TrimSpace(raw) == driftTagPrefix {
			return true
		}
	}
	return false
}
