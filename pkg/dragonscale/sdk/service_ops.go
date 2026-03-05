package sdk

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg"
	"github.com/ZanzyTHEbar/dragonscale/pkg/agent"
	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/channels"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/cron"
	"github.com/ZanzyTHEbar/dragonscale/pkg/devices"
	"github.com/ZanzyTHEbar/dragonscale/pkg/health"
	"github.com/ZanzyTHEbar/dragonscale/pkg/heartbeat"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	dragonruntime "github.com/ZanzyTHEbar/dragonscale/pkg/runtime"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security/securebus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/state"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/voice"
	"github.com/chzyer/readline"
)

func (s *Service) PrintVersion(ctx context.Context, out io.Writer) error {
	_ = ctx
	fmt.Fprintf(out, "%s dragonscale %s\n", s.Logo, s.VersionString())
	build, goVer := s.BuildInfo()
	if build != "" {
		fmt.Fprintf(out, "  Build: %s\n", build)
	}
	if goVer != "" {
		fmt.Fprintf(out, "  Go: %s\n", goVer)
	}
	return nil
}

func (s *Service) LoadConfig() (*config.Config, error) {
	return dragonruntime.LoadResolvedConfig(dragonruntime.LoadConfigOptions{
		BaseConfigPath: s.getConfigPath(),
	})
}

func (s *Service) Onboard(ctx context.Context, in io.Reader, out io.Writer) error {
	_ = ctx
	configPath := s.getConfigPath()

	if _, err := os.Stat(configPath); err == nil {
		fmt.Fprintf(out, "Config already exists at %s\n", configPath)
		fmt.Fprint(out, "Overwrite? (y/n): ")
		response, err := readLine(in)
		if err != nil {
			return err
		}
		if response != "y" {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	cfg := config.DefaultConfig()
	if err := config.SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("error saving config: %w", err)
	}

	s.createWorkspaceTemplates(cfg, out)

	fmt.Fprintf(out, "%s dragonscale is ready!\n", s.Logo)

	fmt.Fprint(out, "\nSet up encrypted secret storage? (y/n): ")
	secretResponse, err := readLine(in)
	if err != nil {
		return err
	}
	if secretResponse == "y" || secretResponse == "Y" {
		key, err := security.GenerateKey()
		if err != nil {
			fmt.Fprintf(out, "Error generating key: %v\n", err)
		} else {
			encoded := fmt.Sprintf("%x", key)
			fmt.Fprintln(out, "\nGenerated master key (keep this safe!):")
			fmt.Fprintln(out, "  "+encoded)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Add to your shell profile:")
			fmt.Fprintln(out, "  export DRAGONSCALE_MASTER_KEY="+encoded)
			fmt.Fprintln(out)
			fmt.Fprintln(out, "Then store secrets with: dragonscale secret add <name>")
		}
	}

	fmt.Fprintln(out, "\nNext steps:")
	fmt.Fprintln(out, "  1. Add your API key to", configPath)
	fmt.Fprintln(out, "     Get one at: https://openrouter.ai/keys")
	fmt.Fprintln(out, "  2. Chat: dragonscale agent -m \"Hello!\"")
	return nil
}

func (s *Service) Agent(ctx context.Context, in io.Reader, out io.Writer, opts AgentOptions) error {
	if opts.Debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Fprintln(out, "🔍 Debug mode enabled")
	}

	sessionKey := opts.SessionKey
	if sessionKey == "" {
		sessionKey = "cli:default"
	}

	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}

	appCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	rt, err := s.BuildAgentRuntime(appCtx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	agentLoop := rt.AgentLoop()
	closeBus := s.AttachSecureBus(agentLoop)
	if closeBus != nil {
		defer closeBus()
	}

	startupInfo := agentLoop.GetStartupInfo()
	logger.InfoCF("agent", "Agent initialized", map[string]interface{}{
		"tools_count":      startupInfo["tools"].(map[string]interface{})["count"],
		"skills_total":     startupInfo["skills"].(map[string]interface{})["total"],
		"skills_available": startupInfo["skills"].(map[string]interface{})["available"],
	})

	if opts.Message != "" {
		response, err := agentLoop.ProcessDirect(appCtx, opts.Message, sessionKey)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "\n%s %s\n", s.Logo, response)
		return nil
	}

	fmt.Fprintf(out, "%s Interactive mode (Ctrl+C to exit)\n\n", s.Logo)
	return s.interactiveMode(appCtx, in, out, agentLoop, sessionKey)
}

func (s *Service) Gateway(ctx context.Context, out io.Writer, opts GatewayOptions) error {
	if opts.Debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Fprintln(out, "🔍 Debug mode enabled")
	}

	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}

	appCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	rt, err := s.BuildAgentRuntime(appCtx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	agentLoop := rt.AgentLoop()
	msgBus := rt.MessageBus()
	closeBus := s.AttachSecureBus(agentLoop)
	if closeBus != nil {
		defer closeBus()
	}

	fmt.Fprintln(out, "\n📦 Agent Status:")
	startupInfo := agentLoop.GetStartupInfo()
	toolsInfo := startupInfo["tools"].(map[string]interface{})
	skillsInfo := startupInfo["skills"].(map[string]interface{})
	fmt.Fprintf(out, "  • Tools: %d loaded\n", toolsInfo["count"])
	fmt.Fprintf(out, "  • Skills: %d/%d available\n", skillsInfo["available"], skillsInfo["total"])

	logger.InfoCF("agent", "Agent initialized", map[string]interface{}{
		"tools_count":      toolsInfo["count"],
		"skills_total":     skillsInfo["total"],
		"skills_available": skillsInfo["available"],
	})

	execTimeout := time.Duration(cfg.Tools.Cron.ExecTimeoutMinutes) * time.Minute
	var cronOpts []cron.CronOption
	if del := agentLoop.MemoryDelegate(); del != nil {
		cronOpts = append(cronOpts, cron.WithCronDelegate(del, pkg.NAME))
	}
	cronService := s.BuildCronTool(appCtx, agentLoop, msgBus, cfg.SandboxPath(), cfg.RestrictToSandbox(), execTimeout, cronOpts...)

	var heartbeatStateOpts []state.Option
	if del := agentLoop.MemoryDelegate(); del != nil {
		heartbeatStateOpts = append(heartbeatStateOpts, state.WithDelegate(del))
	}
	heartbeatService := heartbeat.NewHeartbeatService(cfg.SandboxPath(), cfg.Heartbeat.Interval, cfg.Heartbeat.Enabled, heartbeatStateOpts...)
	heartbeatService.SetBus(msgBus)
	heartbeatService.SetHandler(func(prompt, channel, chatID string) *tools.ToolResult {
		if channel == "" || chatID == "" {
			channel, chatID = "cli", "direct"
		}
		response, err := agentLoop.ProcessHeartbeat(appCtx, prompt, channel, chatID)
		if err != nil {
			return tools.ErrorResult(fmt.Sprintf("Heartbeat error: %v", err))
		}
		if response == "HEARTBEAT_OK" {
			return tools.SilentResult("Heartbeat OK")
		}
		return tools.SilentResult(response)
	})

	if del := agentLoop.MemoryDelegate(); del != nil {
		obligations := tools.NewObligationTool(del, pkg.NAME)
		heartbeatService.SetDueContextProvider(func(now time.Time) (string, error) {
			ctx, cancel := context.WithTimeout(appCtx, 10*time.Second)
			defer cancel()
			due, err := obligations.CollectDueObligations(ctx, now, "heartbeat")
			if err != nil {
				return "", err
			}
			if len(due) == 0 {
				return "", nil
			}
			var b strings.Builder
			b.WriteString("The following obligations are currently due and require action:\n")
			for _, rec := range due {
				dueAt := rec.DueAt
				if dueAt.IsZero() {
					dueAt = rec.ScheduledAt
				}
				dueLabel := "unspecified"
				if !dueAt.IsZero() {
					dueLabel = dueAt.Format(time.RFC3339)
				}
				b.WriteString(fmt.Sprintf("- [%s] %s (state=%s, due_at=%s)\n", rec.ID, rec.Title, rec.State, dueLabel))
				if strings.TrimSpace(rec.Details) != "" {
					b.WriteString(fmt.Sprintf("  details: %s\n", strings.TrimSpace(rec.Details)))
				}
			}
			b.WriteString("For each due obligation, complete the action, then call obligation update_state with executed and obligation add_evidence describing what was done.\n")
			return b.String(), nil
		})
	}

	channelManager, err := channels.NewManager(cfg, msgBus)
	if err != nil {
		fmt.Fprintf(out, "Error creating channel manager: %v\n", err)
		return err
	}

	agent.WithChannelManager(channelManager)(agentLoop)

	var transcriber *voice.GroqTranscriber
	if cfg.Providers.Groq.APIKey != "" {
		transcriber = voice.NewGroqTranscriber(cfg.Providers.Groq.APIKey)
		logger.InfoC("voice", "Groq voice transcription enabled")
	}

	if transcriber != nil {
		if telegramChannel, ok := channelManager.GetChannel("telegram"); ok {
			if tc, ok := telegramChannel.(*channels.TelegramChannel); ok {
				tc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to Telegram channel")
			}
		}
		if discordChannel, ok := channelManager.GetChannel("discord"); ok {
			if dc, ok := discordChannel.(*channels.DiscordChannel); ok {
				dc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to Discord channel")
			}
		}
		if slackChannel, ok := channelManager.GetChannel("slack"); ok {
			if sc, ok := slackChannel.(*channels.SlackChannel); ok {
				sc.SetTranscriber(transcriber)
				logger.InfoC("voice", "Groq transcription attached to Slack channel")
			}
		}
	}

	enabledChannels := channelManager.GetEnabledChannels()
	if len(enabledChannels) > 0 {
		fmt.Fprintf(out, "✓ Channels enabled: %s\n", enabledChannels)
	} else {
		fmt.Fprintln(out, "⚠ Warning: No channels enabled")
	}

	fmt.Fprintf(out, "✓ Gateway started on %s:%d\n", cfg.Gateway.Host, cfg.Gateway.Port)
	fmt.Fprintln(out, "Press Ctrl+C to stop")

	if err := cronService.Start(); err != nil {
		fmt.Fprintf(out, "Error starting cron service: %v\n", err)
	}
	fmt.Fprintln(out, "✓ Cron service started")

	if err := heartbeatService.Start(); err != nil {
		fmt.Fprintf(out, "Error starting heartbeat service: %v\n", err)
	}
	fmt.Fprintln(out, "✓ Heartbeat service started")

	stateManager := state.NewManager(cfg.SandboxPath())
	deviceService := devices.NewService(devices.Config{
		Enabled:    cfg.Devices.Enabled,
		MonitorUSB: cfg.Devices.MonitorUSB,
	}, stateManager)
	deviceService.SetBus(msgBus)
	if err := deviceService.Start(appCtx); err != nil {
		fmt.Fprintf(out, "Error starting device service: %v\n", err)
	} else if cfg.Devices.Enabled {
		fmt.Fprintln(out, "✓ Device event service started")
	}

	if err := channelManager.StartAll(appCtx); err != nil {
		fmt.Fprintf(out, "Error starting channels: %v\n", err)
	}

	healthServer := health.NewServer(cfg.Gateway.Host, cfg.Gateway.Port)
	go func() {
		if err := healthServer.Start(); err != nil && err != http.ErrServerClosed {
			logger.ErrorCF("health", "Health server error", map[string]interface{}{"error": err.Error()})
		}
	}()
	fmt.Fprintf(out, "✓ Health endpoints available at http://%s:%d/health and /ready\n", cfg.Gateway.Host, cfg.Gateway.Port)

	go agentLoop.Run(appCtx)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	select {
	case <-sigChan:
		fmt.Fprintln(out, "\nShutting down...")
	case <-appCtx.Done():
		fmt.Fprintln(out, "\nShutting down...")
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	healthServer.Stop(shutdownCtx)
	deviceService.Stop()
	heartbeatService.Stop()
	cronService.Stop()
	agentLoop.Stop()
	channelManager.StopAll(shutdownCtx)
	fmt.Fprintln(out, "✓ Gateway stopped")
	return nil
}

func (s *Service) Status(ctx context.Context, out io.Writer) error {
	_ = ctx
	cfg, err := s.LoadConfig()
	if err != nil {
		fmt.Fprintf(out, "Error loading config: %v\n", err)
		return err
	}

	configPath := s.getConfigPath()

	fmt.Fprintf(out, "%s dragonscale Status\n", s.Logo)
	fmt.Fprintf(out, "Version: %s\n", s.VersionString())
	build, _ := s.BuildInfo()
	if build != "" {
		fmt.Fprintf(out, "Build: %s\n", build)
	}
	fmt.Fprintln(out)

	if _, err := os.Stat(configPath); err == nil {
		fmt.Println("Config:", configPath, "✓")
	} else {
		fmt.Println("Config:", configPath, "✗")
	}

	sandboxPath := cfg.SandboxPath()
	if _, err := os.Stat(sandboxPath); err == nil {
		fmt.Println("Sandbox:", sandboxPath, "✓")
	} else {
		fmt.Println("Sandbox:", sandboxPath, "✗")
	}

	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Model: %s\n", cfg.Agents.Defaults.Model)

		hasOpenRouter := cfg.Providers.OpenRouter.APIKey != ""
		hasAnthropic := cfg.Providers.Anthropic.APIKey != ""
		hasOpenAI := cfg.Providers.OpenAI.APIKey != ""
		hasGemini := cfg.Providers.Gemini.APIKey != ""
		hasZhipu := cfg.Providers.Zhipu.APIKey != ""
		hasGroq := cfg.Providers.Groq.APIKey != ""
		hasVLLM := cfg.Providers.VLLM.APIBase != ""

		status := func(enabled bool) string {
			if enabled {
				return "✓"
			}
			return "not set"
		}

		fmt.Println("OpenRouter API:", status(hasOpenRouter))
		fmt.Println("Anthropic API:", status(hasAnthropic))
		fmt.Println("OpenAI API:", status(hasOpenAI))
		fmt.Println("Gemini API:", status(hasGemini))
		fmt.Println("Zhipu API:", status(hasZhipu))
		fmt.Println("Groq API:", status(hasGroq))
		if hasVLLM {
			fmt.Printf("vLLM/Local: ✓ %s\n", cfg.Providers.VLLM.APIBase)
		} else {
			fmt.Println("vLLM/Local: not set")
		}

		store, _ := auth.LoadStore()
		if store != nil && len(store.Credentials) > 0 {
			fmt.Println("\nOAuth/Token Auth:")
			for provider, cred := range store.Credentials {
				statusMsg := "authenticated"
				if cred.IsExpired() {
					statusMsg = "expired"
				} else if cred.NeedsRefresh() {
					statusMsg = "needs refresh"
				}
				fmt.Printf("  %s (%s): %s\n", provider, cred.AuthMethod, statusMsg)
			}
		}

		fmt.Println("\nMemory System:")
		fmt.Printf("  Embedding dims: %d\n", cfg.Memory.EmbeddingDims)
		if cfg.Memory.Embedding.Provider != "" {
			fmt.Printf("  Embedding provider: %s\n", cfg.Memory.Embedding.Provider)
			if cfg.Memory.Embedding.Model != "" {
				fmt.Printf("  Embedding model: %s\n", cfg.Memory.Embedding.Model)
			}
		} else {
			fmt.Println("  Embedding provider: none (FTS5-only)")
		}
		if cfg.Memory.Sync.SyncURL != "" {
			fmt.Printf("  Turso replica: ✓ %s\n", cfg.Memory.Sync.SyncURL)
			fmt.Printf("  Sync interval: %ds\n", cfg.Memory.Sync.SyncIntervalSeconds)
		} else {
			fmt.Println("  Turso replica: local-only")
		}

		memDBPath := cfg.Memory.DBPath
		if memDBPath == "" {
			memDBPath = filepath.Join(cfg.SandboxPath(), "memory", "dragonscale.db")
		}
		if fi, err := os.Stat(memDBPath); err == nil {
			fmt.Printf("  DB size: %.1f KB\n", float64(fi.Size())/1024)
		}
	}

	return nil
}

func (s *Service) AuthLogin(ctx context.Context, in io.Reader, out io.Writer, provider string, useDeviceCode bool) error {
	_ = in
	if provider == "" {
		fmt.Fprintln(out, "Error: --provider is required")
		fmt.Fprintln(out, "Supported providers: openai, anthropic")
		return fmt.Errorf("provider is required")
	}

	cfgPath := s.getConfigPath()

	switch provider {
	case "openai":
		cfg := auth.OpenAIOAuthConfig()
		var cred *auth.AuthCredential
		var err error
		if useDeviceCode {
			cred, err = auth.LoginDeviceCode(cfg)
		} else {
			cred, err = auth.LoginBrowser(cfg)
		}
		if err != nil {
			return err
		}
		if err := auth.SetCredential("openai", cred); err != nil {
			return err
		}
		appCfg, err := s.LoadConfig()
		if err == nil {
			appCfg.Providers.OpenAI.AuthMethod = "oauth"
			if err := config.SaveConfig(cfgPath, appCfg); err != nil {
				fmt.Fprintf(out, "Warning: could not update config: %v\n", err)
			}
		}
		fmt.Fprintln(out, "Login successful!")
		if cred.AccountID != "" {
			fmt.Fprintf(out, "Account: %s\n", cred.AccountID)
		}
	case "anthropic":
		cred, err := auth.LoginPasteToken(provider, in)
		if err != nil {
			return err
		}
		if err := auth.SetCredential(provider, cred); err != nil {
			return err
		}
		appCfg, err := s.LoadConfig()
		if err == nil {
			appCfg.Providers.Anthropic.AuthMethod = "token"
			if err := config.SaveConfig(cfgPath, appCfg); err != nil {
				fmt.Fprintf(out, "Warning: could not update config: %v\n", err)
			}
		}
		fmt.Fprintf(out, "Token saved for %s!\n", provider)
	default:
		fmt.Fprintf(out, "Unsupported provider: %s\n", provider)
		fmt.Fprintln(out, "Supported providers: openai, anthropic")
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	return nil
}

func (s *Service) AuthLogout(ctx context.Context, out io.Writer, provider string) error {
	_ = ctx
	cfgPath := s.getConfigPath()
	if provider != "" {
		if err := auth.DeleteCredential(provider); err != nil {
			return err
		}
		appCfg, err := s.LoadConfig()
		if err == nil {
			switch provider {
			case "openai":
				appCfg.Providers.OpenAI.AuthMethod = ""
			case "anthropic":
				appCfg.Providers.Anthropic.AuthMethod = ""
			}
			config.SaveConfig(cfgPath, appCfg)
		}
		fmt.Fprintf(out, "Logged out from %s\n", provider)
		return nil
	}

	if err := auth.DeleteAllCredentials(); err != nil {
		return err
	}
	appCfg, err := s.LoadConfig()
	if err == nil {
		appCfg.Providers.OpenAI.AuthMethod = ""
		appCfg.Providers.Anthropic.AuthMethod = ""
		_ = config.SaveConfig(cfgPath, appCfg)
	}
	fmt.Fprintln(out, "Logged out from all providers")
	return nil
}

func (s *Service) AuthStatus(ctx context.Context, out io.Writer) error {
	_ = ctx
	store, err := auth.LoadStore()
	if err != nil {
		fmt.Fprintf(out, "Error loading auth store: %v\n", err)
		return err
	}
	if len(store.Credentials) == 0 {
		fmt.Fprintln(out, "No authenticated providers.")
		fmt.Fprintln(out, "Run: dragonscale auth login --provider <name>")
		return nil
	}

	fmt.Fprintln(out, "\nAuthenticated Providers:")
	fmt.Fprintln(out, "------------------------")
	for provider, cred := range store.Credentials {
		status := "active"
		if cred.IsExpired() {
			status = "expired"
		} else if cred.NeedsRefresh() {
			status = "needs refresh"
		}
		fmt.Fprintf(out, "  %s:\n", provider)
		fmt.Fprintf(out, "    Method: %s\n", cred.AuthMethod)
		fmt.Fprintf(out, "    Status: %s\n", status)
		if cred.AccountID != "" {
			fmt.Fprintf(out, "    Account: %s\n", cred.AccountID)
		}
		if !cred.ExpiresAt.IsZero() {
			fmt.Fprintf(out, "    Expires: %s\n", cred.ExpiresAt.Format("2006-01-02 15:04"))
		}
	}
	return nil
}

func (s *Service) CronList(ctx context.Context, out io.Writer) error {
	_ = ctx
	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	storePath := filepath.Join(cfg.SandboxPath(), "cron", "jobs.json")
	cs := cron.NewCronService(storePath, nil)
	jobs := cs.ListJobs(true)
	if len(jobs) == 0 {
		fmt.Fprintln(out, "No scheduled jobs.")
		return nil
	}

	fmt.Fprintln(out, "\nScheduled Jobs:")
	fmt.Fprintln(out, "----------------")
	for _, job := range jobs {
		var schedule string
		if job.Schedule.Kind == "every" && job.Schedule.EveryMS != nil {
			schedule = fmt.Sprintf("every %ds", *job.Schedule.EveryMS/1000)
		} else if job.Schedule.Kind == "cron" {
			schedule = job.Schedule.Expr
		} else {
			schedule = "one-time"
		}
		nextRun := "scheduled"
		if job.State.NextRunAtMS != nil {
			nextTime := time.UnixMilli(*job.State.NextRunAtMS)
			nextRun = nextTime.Format("2006-01-02 15:04")
		}
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}
		fmt.Fprintf(out, "  %s (%s)\n", job.Name, job.ID)
		fmt.Fprintf(out, "    Schedule: %s\n", schedule)
		fmt.Fprintf(out, "    Status: %s\n", status)
		fmt.Fprintf(out, "    Next run: %s\n", nextRun)
	}
	return nil
}

func (s *Service) CronAdd(ctx context.Context, out io.Writer, opts CronAddOptions) error {
	_ = ctx
	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	storePath := filepath.Join(cfg.SandboxPath(), "cron", "jobs.json")
	cs := cron.NewCronService(storePath, nil)
	job, err := cs.AddJob(opts.Name, s.scheduleFromOptions(opts), opts.Message, opts.Deliver, opts.Channel, opts.To)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ Added job '%s' (%s)\n", job.Name, job.ID)
	return nil
}

func (s *Service) CronRemove(ctx context.Context, out io.Writer, jobID string) error {
	_ = ctx
	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	storePath := filepath.Join(cfg.SandboxPath(), "cron", "jobs.json")
	cs := cron.NewCronService(storePath, nil)
	if cs.RemoveJob(jobID) {
		fmt.Fprintf(out, "✓ Removed job %s\n", jobID)
	} else {
		fmt.Fprintf(out, "✗ Job %s not found\n", jobID)
	}
	return nil
}

func (s *Service) CronEnable(ctx context.Context, out io.Writer, jobID string, enabled bool) error {
	_ = ctx
	cfg, err := s.LoadConfig()
	if err != nil {
		return err
	}
	storePath := filepath.Join(cfg.SandboxPath(), "cron", "jobs.json")
	cs := cron.NewCronService(storePath, nil)
	job := cs.EnableJob(jobID, enabled)
	if job != nil {
		status := "enabled"
		if !enabled {
			status = "disabled"
		}
		fmt.Fprintf(out, "✓ Job '%s' %s\n", job.Name, status)
	} else {
		fmt.Fprintf(out, "✗ Job %s not found\n", jobID)
	}
	return nil
}

func (s *Service) scheduleFromOptions(opts CronAddOptions) cron.CronSchedule {
	if opts.EveryMS != nil {
		return cron.CronSchedule{Kind: "every", EveryMS: opts.EveryMS}
	}
	return cron.CronSchedule{Kind: "cron", Expr: opts.Cron}
}

func (s *Service) createWorkspaceTemplates(cfg *config.Config, out io.Writer) {
	identityDir, err := config.IdentityDir()
	if err != nil {
		fmt.Fprintf(out, "Error resolving identity dir: %v\n", err)
		return
	}
	if err := s.seedEmbeddedIdentity(identityDir); err != nil {
		fmt.Fprintf(out, "Error seeding identity files: %v\n", err)
	}

	skillsDir, err := config.SkillsDir()
	if err != nil {
		fmt.Fprintf(out, "Error resolving skills dir: %v\n", err)
		return
	}
	if err := s.seedEmbeddedSkills(skillsDir); err != nil {
		fmt.Fprintf(out, "Error seeding skills: %v\n", err)
	}

	_ = os.MkdirAll(cfg.SandboxPath(), 0755)
}

func (s *Service) seedEmbeddedIdentity(identityDir string) error {
	if s.EmbeddedFS == nil {
		return nil
	}
	identityFiles := []string{"AGENT.md", "IDENTITY.md", "SOUL.md", "USER.md"}
	for _, name := range identityFiles {
		data, err := fs.ReadFile(s.EmbeddedFS, "workspace/"+name)
		if err != nil {
			continue
		}
		targetPath := filepath.Join(identityDir, name)
		if _, err := os.Stat(targetPath); err == nil {
			continue
		}
		if err := os.WriteFile(targetPath, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", targetPath, err)
		}
	}
	return nil
}

func (s *Service) seedEmbeddedSkills(skillsDir string) error {
	if s.EmbeddedFS == nil {
		return nil
	}
	return fs.WalkDir(s.EmbeddedFS, "workspace/skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := fs.ReadFile(s.EmbeddedFS, path)
		if readErr != nil {
			return readErr
		}
		relPath, _ := filepath.Rel("workspace/skills", path)
		targetPath := filepath.Join(skillsDir, relPath)
		if _, statErr := os.Stat(targetPath); statErr == nil {
			return nil
		}
		_ = os.MkdirAll(filepath.Dir(targetPath), 0755)
		return os.WriteFile(targetPath, data, 0644)
	})
}

func (s *Service) getConfigPath() string {
	if p, err := config.DefaultConfigPath(); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dragonscale", "config.json")
}

func (s *Service) CopyDirectory(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}

func (s *Service) interactiveMode(ctx context.Context, in io.Reader, out io.Writer, agentLoop *agent.AgentLoop, sessionKey string) error {
	prompt := fmt.Sprintf("%s You: ", s.Logo)
	reader := bufio.NewReader(os.Stdin)
	if in != nil {
		reader = bufio.NewReader(in)
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     filepath.Join(os.TempDir(), ".dragonscale_history"),
		HistoryLimit:    100,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})

	if err == nil {
		defer rl.Close()
		for {
			line, readErr := rl.Readline()
			if readErr != nil {
				if readErr == readline.ErrInterrupt || readErr == io.EOF {
					fmt.Fprintln(out, "\nGoodbye!")
					return nil
				}
				fmt.Fprintf(out, "Error reading input: %v\n", readErr)
				continue
			}
			input := strings.TrimSpace(line)
			if input == "" {
				continue
			}
			if input == "exit" || input == "quit" {
				fmt.Fprintln(out, "Goodbye!")
				return nil
			}
			response, respErr := agentLoop.ProcessDirect(ctx, input, sessionKey)
			if respErr != nil {
				fmt.Fprintf(out, "Error: %v\n", respErr)
				continue
			}
			fmt.Fprintf(out, "\n%s %s\n\n", s.Logo, response)
		}
	}

	fmt.Fprintln(out, "Falling back to simple input mode...")
	for {
		fmt.Fprint(out, prompt)
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr == io.EOF {
				fmt.Fprintln(out, "\nGoodbye!")
				return nil
			}
			fmt.Fprintf(out, "Error reading input: %v\n", readErr)
			continue
		}
		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Fprintln(out, "Goodbye!")
			return nil
		}
		response, respErr := agentLoop.ProcessDirect(ctx, input, sessionKey)
		if respErr != nil {
			fmt.Fprintf(out, "Error: %v\n", respErr)
			continue
		}
		fmt.Fprintf(out, "\n%s %s\n\n", s.Logo, response)
	}
}

// BuildAgentRuntime returns a configured runtime handle for command and adapter operations.
func (s *Service) BuildAgentRuntime(appCtx context.Context, cfg *config.Config) (*dragonruntime.RuntimeHandle, error) {
	return s.bootstrapAgentRuntime(appCtx, cfg)
}

// AttachSecureBus configures secure bus support for the provided agent loop and returns a close callback.
func (s *Service) AttachSecureBus(agentLoop *agent.AgentLoop) func() {
	return s.setupSecureBus(agentLoop)
}

// BuildCronTool registers cron tooling on the agent runtime and returns the cron service.
func (s *Service) BuildCronTool(appCtx context.Context, agentLoop *agent.AgentLoop, msgBus *bus.MessageBus, workspace string, restrictToSandbox bool, execTimeout time.Duration, cronOpts ...cron.CronOption) *cron.CronService {
	return s.setupCronTool(appCtx, agentLoop, msgBus, workspace, restrictToSandbox, execTimeout, cronOpts...)
}

// DaemonSocketPath returns the active daemon socket path used by the current environment.
func (s *Service) DaemonSocketPath() string {
	return s.daemonSocketPath()
}

// DaemonPIDPath returns the active daemon PID file path used by the current environment.
func (s *Service) DaemonPIDPath() string {
	return s.daemonPIDPath()
}

// ConfigPath resolves the active config path for this service.
func (s *Service) ConfigPath() string {
	return s.getConfigPath()
}

func (s *Service) setupCronTool(appCtx context.Context, agentLoop *agent.AgentLoop, msgBus *bus.MessageBus, workspace string, restrict bool, execTimeout time.Duration, cronOpts ...cron.CronOption) *cron.CronService {
	cronStorePath := filepath.Join(workspace, "cron", "jobs.json")

	cronService := cron.NewCronService(cronStorePath, nil, cronOpts...)
	cronTool := tools.NewCronTool(cronService, agentLoop, msgBus, workspace, restrict, execTimeout)
	agentLoop.RegisterTool(cronTool)

	cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		jobCtx, jobCancel := context.WithTimeout(appCtx, execTimeout)
		defer jobCancel()
		result := cronTool.ExecuteJob(jobCtx, job)
		return result, nil
	})

	return cronService
}

func (s *Service) bootstrapAgentRuntime(appCtx context.Context, cfg *config.Config) (*dragonruntime.RuntimeHandle, error) {
	return dragonruntime.Bootstrap(appCtx, cfg, dragonruntime.BootstrapOptions{
		OutboundMode: dragonruntime.OutboundModeNone,
	})
}

func (s *Service) setupSecureBus(agentLoop *agent.AgentLoop) (closer func()) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			logger.WarnC("itr", "SecureBus: cannot determine config dir — running without ITR")
			return func() {}
		}
		cfgDir = filepath.Join(home, ".dragonscale")
	}

	secretsPath := filepath.Join(cfgDir, "secrets.json")
	keyring := security.NewEnvKeyring(security.MasterKeyEnvVar)
	ss, err := security.NewSecretStore(secretsPath, keyring)
	if err != nil {
		logger.WarnCF("itr", "SecureBus: failed to load secret store — running without secret injection", map[string]interface{}{"error": err.Error()})
		ss = nil
	}
	if os.Getenv(security.MasterKeyEnvVar) == "" {
		logger.WarnCF("itr", "SecureBus: master key env var is not set; secret injection requiring stored secrets will fail", map[string]interface{}{"env_var": security.MasterKeyEnvVar})
	}

	bus := agentLoop.SetupSecureBus(ss, securebus.DefaultBusConfig())
	logger.InfoC("itr", "SecureBus enabled — tool calls routed through ITR")
	return bus.Close
}

func (s *Service) daemonSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dragonscale", "daemon.sock")
}

func (s *Service) daemonPIDPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dragonscale", "daemon.pid")
}

func readLine(in io.Reader) (string, error) {
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
