package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

func TestEmptyPromptTraceEmitsEmptyStepsArray(t *testing.T) {
	trace := emptyPromptTrace("   ")
	if trace == nil {
		t.Fatal("expected empty prompt trace")
	}

	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal trace: %v", err)
	}

	if !strings.Contains(string(raw), `"steps":[]`) {
		t.Fatalf("expected empty steps array, got %s", string(raw))
	}
}

func TestStabilizeEvalConfigClampsAgentDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Temperature = 0.7
	cfg.Agents.Defaults.MaxToolIterations = 20

	stabilizeEvalConfig(cfg)

	if cfg.Agents.Defaults.Temperature != 0 {
		t.Fatalf("expected eval temperature 0, got %v", cfg.Agents.Defaults.Temperature)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 8 {
		t.Fatalf("expected max tool iterations 8, got %d", cfg.Agents.Defaults.MaxToolIterations)
	}
}
