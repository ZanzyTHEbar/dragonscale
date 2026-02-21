package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

const EvalConfigEnvVar = "PICOCLAW_EVAL_CONFIG"

type LoadConfigOptions struct {
	BaseConfigPath     string
	OverlayConfigPath  string
	MinProviderTimeout time.Duration
}

func ResolveBaseConfigPath() string {
	// Prefer XDG standard path (~/.config/picoclaw/config.json) when present.
	if xdgPath, err := config.DefaultConfigPath(); err == nil {
		if _, statErr := os.Stat(xdgPath); statErr == nil {
			return xdgPath
		}
	}

	home, _ := os.UserHomeDir()
	legacy := filepath.Join(home, ".picoclaw", "config.json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}

	// Neither exists; return XDG path if resolvable so defaults still load.
	if xdgPath, err := config.DefaultConfigPath(); err == nil {
		return xdgPath
	}
	return legacy
}

func LoadResolvedConfig(opts LoadConfigOptions) (*config.Config, error) {
	basePath := opts.BaseConfigPath
	if basePath == "" {
		basePath = ResolveBaseConfigPath()
	}

	cfg, err := config.LoadConfig(basePath)
	if err != nil {
		return nil, fmt.Errorf("load base config: %w", err)
	}

	if opts.OverlayConfigPath != "" {
		if err := config.OverlayConfigFile(cfg, opts.OverlayConfigPath); err != nil {
			return nil, fmt.Errorf("load config overlay: %w", err)
		}
	}

	EnsureMinProviderTimeout(cfg, opts.MinProviderTimeout)
	return cfg, nil
}

func LoadEvalConfig(minTimeout time.Duration) (*config.Config, error) {
	return LoadResolvedConfig(LoadConfigOptions{
		OverlayConfigPath:  os.Getenv(EvalConfigEnvVar),
		MinProviderTimeout: minTimeout,
	})
}

func EnsureMinProviderTimeout(cfg *config.Config, minTimeout time.Duration) {
	if cfg == nil || minTimeout <= 0 {
		return
	}
	minSeconds := int(minTimeout.Seconds())
	if minSeconds <= 0 {
		return
	}

	set := func(p *config.ProviderConfig) {
		if p.Timeout == 0 || p.Timeout < minSeconds {
			p.Timeout = minSeconds
		}
	}

	providers := []*config.ProviderConfig{
		&cfg.Providers.Anthropic,
		&cfg.Providers.OpenAI.ProviderConfig,
		&cfg.Providers.OpenRouter,
		&cfg.Providers.Groq,
		&cfg.Providers.Zhipu,
		&cfg.Providers.VLLM,
		&cfg.Providers.Gemini,
		&cfg.Providers.Nvidia,
		&cfg.Providers.Ollama,
		&cfg.Providers.Moonshot,
		&cfg.Providers.ShengSuanYun,
		&cfg.Providers.DeepSeek,
		&cfg.Providers.GitHubCopilot,
	}
	for _, p := range providers {
		set(p)
	}
}
