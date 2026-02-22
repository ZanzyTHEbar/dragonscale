package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

const (
	EvalConfigEnvVar     = "DRAGONSCALE_EVAL_CONFIG"
	EvalBaseConfigEnvVar = "DRAGONSCALE_EVAL_BASE_CONFIG"
	EvalHostHomeEnvVar   = "DRAGONSCALE_EVAL_HOST_HOME"
)

type LoadConfigOptions struct {
	BaseConfigPath     string
	OverlayConfigPath  string
	MinProviderTimeout time.Duration
}

func ResolveBaseConfigPath() string {
	if explicit := os.Getenv(EvalBaseConfigEnvVar); explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit
		}
	}

	checkHostConfig := func(hostHome string) string {
		if hostHome == "" {
			return ""
		}
		// XDG standard path.
		hostXDG := filepath.Join(hostHome, ".config", pkg.NAME, "config.json")
		if _, err := os.Stat(hostXDG); err == nil {
			return hostXDG
		}

		return ""
	}

	// Prefer host-mounted config when running in containerized eval.
	if hostHome := os.Getenv(EvalHostHomeEnvVar); hostHome != "" {
		if path := checkHostConfig(hostHome); path != "" {
			return path
		}
	}

	// Fall back to the default devcontainer host mount location.
	if path := checkHostConfig("/host_home"); path != "" {
		return path
	}

	// Prefer XDG standard path (~/.config/dragonscale/config.json) when present.
	xdgPath, err := config.DefaultConfigPath()
	if err == nil {
		if _, statErr := os.Stat(xdgPath); statErr == nil {
			return xdgPath
		}
	}

	// Neither exists; return XDG path if resolvable so defaults still load.
	if err == nil {
		return xdgPath
	}

	return ""
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
