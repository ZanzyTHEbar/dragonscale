package agent

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ZanzyTHEbar/dragonscale/pkg/bus"
	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
	"github.com/ZanzyTHEbar/dragonscale/pkg/logger"
	"github.com/ZanzyTHEbar/dragonscale/pkg/messages"
	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	"github.com/ZanzyTHEbar/dragonscale/pkg/tools"
	"github.com/ZanzyTHEbar/dragonscale/pkg/utils"
)

func initSecretStore() (*security.SecretStore, error) {
	cfgDir, err := config.ConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}

	secretsPath := filepath.Join(cfgDir, "secrets.json")
	keyring := security.NewEnvKeyring(security.MasterKeyEnvVar)
	ss, err := security.NewSecretStore(secretsPath, keyring)
	if err != nil {
		return nil, fmt.Errorf("initialize secret store: %w", err)
	}

	if os.Getenv(security.MasterKeyEnvVar) == "" {
		logger.WarnCF("security", "master key env var is not set; secret injection requiring stored secrets will fail",
			map[string]interface{}{"env_var": security.MasterKeyEnvVar})
	}
	return ss, nil
}

// createToolRegistry creates a tool registry with common tools.
// createToolRegistry builds the base tool set (filesystem, shell, web, etc.).
// Parent and subagent registries start from the same base; memory/search/skill
// tools are registered separately on each so they have isolated discovery state.
func createToolRegistry(workspace string, restrict bool, cfg *config.Config, msgBus *bus.MessageBus) *tools.ToolRegistry {
	registry := tools.NewToolRegistry()

	// File system tools
	registry.Register(tools.NewReadFileTool(workspace, restrict))
	registry.Register(tools.NewWriteFileTool(workspace, restrict))
	registry.Register(tools.NewListDirTool(workspace, restrict))
	registry.Register(tools.NewEditFileTool(workspace, restrict))
	registry.Register(tools.NewAppendFileTool(workspace, restrict))

	// Shell execution
	registry.Register(tools.NewExecTool(workspace, restrict))

	if searchTool := tools.NewWebSearchTool(tools.WebSearchToolOptions{
		BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
		PerplexityAPIKey:     cfg.Tools.Web.Perplexity.APIKey,
		PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
		PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
	}); searchTool != nil {
		registry.Register(searchTool)
	}
	registry.Register(tools.NewWebFetchTool(50000))

	// Hardware tools (I2C, SPI) - Linux only, returns error on other platforms
	registry.Register(tools.NewI2CTool())
	registry.Register(tools.NewSPITool())

	// Message tool - available to both agent and subagent
	// Subagent uses it to communicate directly with user
	messageTool := tools.NewMessageTool()
	messageTool.SetSendCallback(func(channel, chatID, content string) error {
		msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: content,
		})
		return nil
	})
	registry.Register(messageTool)

	return registry
}

// formatMessagesForLog formats messages for logging
func formatMessagesForLog(msgs []messages.Message) string {
	if len(msgs) == 0 {
		return "[]"
	}

	var result string
	result += "[\n"
	for i, msg := range msgs {
		result += fmt.Sprintf("  [%d] Role: %s\n", i, msg.Role)
		if len(msg.ToolCalls) > 0 {
			result += "  ToolCalls:\n"
			for _, tc := range msg.ToolCalls {
				result += fmt.Sprintf("    - ID: %s, Type: %s, Name: %s\n", tc.ID, tc.Type, tc.Name)
				if tc.Function != nil {
					result += fmt.Sprintf("      Arguments: %s\n", utils.Truncate(tc.Function.Arguments, 200))
				}
			}
		}
		if msg.Content != "" {
			content := utils.Truncate(msg.Content, 200)
			result += fmt.Sprintf("  Content: %s\n", content)
		}
		if msg.ToolCallID != "" {
			result += fmt.Sprintf("  ToolCallID: %s\n", msg.ToolCallID)
		}
		result += "\n"
	}
	result += "]"
	return result
}

func toolNames(tt []tools.Tool) []string {
	names := make([]string, len(tt))
	for i, t := range tt {
		names[i] = t.Name()
	}
	return names
}
