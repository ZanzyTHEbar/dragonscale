package agent

import (
	"strings"
	"testing"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

func TestListConfiguredModelsShowsSourceAndHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "gpt-5.5"

	got := listConfiguredModels(cfg)
	for _, want := range []string{"Current model: gpt-5.5", "No providers configured", "DRAGONSCALE_PROVIDERS_OPENAI_API_KEY"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}

func TestListConfiguredModelsIncludesProviderSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DRAGONSCALE_PROVIDERS_OPENAI_API_KEY", "env-key")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "gpt-5.5"
	cfg.Providers.OpenAI.APIKey = "env-key"
	cfg.Providers.OpenCode.APIKey = "config-key"

	got := listConfiguredModels(cfg)
	for _, want := range []string{"Current model: gpt-5.5 (provider: openai)", "openai(env)", "opencode-go(config)", "opencode-zen(config)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}
