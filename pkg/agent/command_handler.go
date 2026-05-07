package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/ZanzyTHEbar/dragonscale/pkg/auth"
	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

// SlashCommand defines an in-agent command.
type SlashCommand struct {
	Name        string
	Description string
	Usage       string
	Handler     func(al *AgentLoop, ctx context.Context, msg bus.InboundMessage, args []string) string
}

// listConfiguredModels returns a human-readable summary of which providers
// have API credentials configured, and the current default model.
func listConfiguredModels(cfg *config.Config) string {
	if cfg == nil {
		return "No configuration available."
	}

	current := fmt.Sprintf("Current model: %s", cfg.Agents.Defaults.Model)
	if cfg.Agents.Defaults.Provider != "" {
		current += fmt.Sprintf(" (provider: %s)", cfg.Agents.Defaults.Provider)
	}

	configuredInfos := cfg.Providers.ConfiguredProviderInfos()
	if chatGPTOAuthConfigured() {
		configuredInfos = append(configuredInfos, config.ConfiguredProviderInfo{Name: "chatgpt", Source: "auth"})
	}
	if len(configuredInfos) == 0 {
		return current + "\nNo providers configured — set API keys in config.json or environment variables. Hint: set DRAGONSCALE_PROVIDERS_OPENAI_API_KEY or run dragonscale auth login --provider openai for ChatGPT OAuth."
	}

	configured := make([]string, 0, len(configuredInfos))
	for _, info := range configuredInfos {
		configured = append(configured, fmt.Sprintf("%s(%s)", info.Name, info.Source))
	}
	return current + "\nConfigured providers: " + strings.Join(configured, ", ")
}

func chatGPTOAuthConfigured() bool {
	cred, err := auth.GetCredential("openai")
	if err != nil || cred == nil || cred.AuthMethod != "oauth" {
		return false
	}
	return cred.RefreshToken != "" || (cred.AccessToken != "" && !cred.IsExpired())
}

func defaultSlashCommands() []SlashCommand {
	return []SlashCommand{
		{
			Name:        "/show",
			Description: "Display current settings.",
			Usage:       "/show [model|channel]",
			Handler: func(al *AgentLoop, _ context.Context, msg bus.InboundMessage, args []string) string {
				if len(args) < 1 {
					return "Usage: /show [model|channel]"
				}
				switch args[0] {
				case "model":
					return fmt.Sprintf("Current model: %s", al.model)
				case "channel":
					parts := []string{fmt.Sprintf("Current channel: %s", msg.Channel)}
					if override, ok := al.getOutputTarget(); ok {
						parts = append(parts, fmt.Sprintf("Output redirect: %s:%s", override.Channel, override.ChatID))
					} else {
						parts = append(parts, "Output redirect: default")
					}
					return strings.Join(parts, "\n")
				default:
					return fmt.Sprintf("Unknown show target: %s", args[0])
				}
			},
		},
		{
			Name:        "/list",
			Description: "List configured providers or enabled channels.",
			Usage:       "/list [models|channels]",
			Handler: func(al *AgentLoop, _ context.Context, _ bus.InboundMessage, args []string) string {
				if len(args) < 1 {
					return "Usage: /list [models|channels]"
				}
				switch args[0] {
				case "models":
					return listConfiguredModels(al.cfg)
				case "channels":
					if al.channelManager == nil {
						return "Channel manager not initialized"
					}
					channels := al.channelManager.GetEnabledChannels()
					if len(channels) == 0 {
						return "No channels enabled"
					}
					return fmt.Sprintf("Enabled channels: %s", strings.Join(channels, ", "))
				default:
					return fmt.Sprintf("Unknown list target: %s", args[0])
				}
			},
		},
		{
			Name:        "/switch",
			Description: "Switch model or target channel alias.",
			Usage:       "/switch [model|channel] to <name>",
			Handler: func(al *AgentLoop, ctx context.Context, _ bus.InboundMessage, args []string) string {
				if len(args) < 3 || args[1] != "to" {
					return "Usage: /switch [model|channel] to <name>"
				}
				target := args[0]
				value := args[2]
				switch target {
				case "model":
					oldModel := al.model
					al.model = value
					return fmt.Sprintf("Switched model from %s to %s", oldModel, value)
				case "channel":
					return al.handleSwitchChannel(ctx, value)
				default:
					return fmt.Sprintf("Unknown switch target: %s", target)
				}
			},
		},
		{
			Name:        "/help",
			Description: "List available slash commands.",
			Usage:       "/help [command]",
			Handler: func(al *AgentLoop, _ context.Context, _ bus.InboundMessage, args []string) string {
				if len(args) == 0 {
					lines := make([]string, 0, len(al.commandRegistry)+1)
					lines = append(lines, "Available slash commands:")
					for _, cmd := range al.commandRegistry {
						lines = append(lines, fmt.Sprintf("  %s - %s (%s)", cmd.Usage, cmd.Description, cmd.Name))
					}
					return strings.Join(lines, "\n")
				}
				target := args[0]
				if !strings.HasPrefix(target, "/") {
					target = "/" + target
				}
				for _, cmd := range al.commandRegistry {
					if cmd.Name == target {
						return fmt.Sprintf("%s - %s", cmd.Usage, cmd.Description)
					}
				}
				return fmt.Sprintf("Unknown command: %s", target)
			},
		},
	}
}

func (al *AgentLoop) parseSwitchChannelTarget(target string) (string, string) {
	channel := strings.TrimSpace(target)
	chatID := ""
	if idx := strings.Index(channel, ":"); idx >= 0 {
		chatID = strings.TrimSpace(channel[idx+1:])
		channel = strings.TrimSpace(channel[:idx])
	}
	return channel, chatID
}

func (al *AgentLoop) handleSwitchChannel(ctx context.Context, target string) string {
	channel, chatID := al.parseSwitchChannelTarget(target)
	if channel == "" {
		return "Usage: /switch channel to <name>[:chat_id]"
	}

	if channel == "cli" {
		al.outputOverride.Store(outputTarget{})
		if al.state != nil {
			_ = al.state.SetChannelAndChatID(ctx, "cli", "")
		}
		return "Cleared output channel override to CLI defaults"
	}

	if al.channelManager == nil {
		return "Channel manager not initialized"
	}
	if _, exists := al.channelManager.GetChannel(channel); !exists {
		return fmt.Sprintf("Channel '%s' not found or not enabled", channel)
	}

	if chatID == "" {
		if al.state == nil {
			return "No chat ID available for channel target. Use /switch channel to <channel:chat_id>"
		}
		chatID = al.state.GetLastChatID()
	}
	if chatID == "" {
		return "No chat ID available for channel target. Use /switch channel to <channel:chat_id>"
	}

	al.outputOverride.Store(outputTarget{
		Channel: channel,
		ChatID:  chatID,
	})
	if al.state != nil {
		if err := al.state.SetChannelAndChatID(ctx, channel, chatID); err != nil {
			return fmt.Sprintf("Output redirection set to %s:%s, but failed to persist state: %v", channel, chatID, err)
		}
	}

	return fmt.Sprintf("Switched target channel to %s:%s", channel, chatID)
}

// handleCommand processes slash commands and returns (response, handled).
func (al *AgentLoop) handleCommand(ctx context.Context, msg bus.InboundMessage) (string, bool) {
	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "/") {
		return "", false
	}

	parts := strings.Fields(content)
	if len(parts) == 0 {
		return "", false
	}

	cmd := parts[0]
	args := parts[1:]

	for _, command := range al.commandRegistry {
		if command.Name != cmd {
			continue
		}
		if command.Handler == nil {
			return "Command is not implemented", true
		}
		return command.Handler(al, ctx, msg, args), true
	}

	return "", false
}
