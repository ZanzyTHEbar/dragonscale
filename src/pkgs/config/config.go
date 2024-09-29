package config

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type DebugLevelType string

const (
	DebugLevelInfo  DebugLevelType = "info"
	DebugLevelDebug DebugLevelType = "debug"
	DebugLevelWarn  DebugLevelType = "warn"
	DebugLevelError DebugLevelType = "error"
	DebugLevelTrace DebugLevelType = "trace"
	DebugLevelOff   DebugLevelType = "off"
)

type Logger struct {
	Style string         `mapstructure:"style"`
	Level DebugLevelType `mapstructure:"level"`
}

// Config holds the mapping of file types to extensions
type Config struct {
	HttpPort      int    `mapstructure:"http_port"`
	DBHost        string `mapstructure:"db_host"`
	DBPort        int    `mapstructure:"db_port"`
	DBName        string `mapstructure:"db_name"`
	DBUser        string `mapstructure:"db_user"`
	CacheDir      string `mapstructure:"cache_dir"`
	SessionSecret string `mapstructure:"session_secret"`
	Logger        Logger `mapstructure:"logger"`
	cfg           *viper.Viper
}

const ConfigFileName = "goflexpro"

func NewConfig(path *string, encryptionKey *string) (Config, error) {
	cfg := viper.New()

	cfg.SetConfigName(fmt.Sprintf(".%s", ConfigFileName)) // name of config file (without extension)
	//cfg.SetConfigType("toml")         // REQUIRED if the config file does not have the extension in the name

	var defaultConfigPath = filepath.Join(os.Getenv("HOME"), ".config", ConfigFileName)

	if path != nil {
		cfg.SetConfigFile(*path)
	} else {
		log.Printf("Config file not provided. Looking for config file in default locations\n")
		cfg.AddConfigPath(filepath.Join("/etc", ConfigFileName)) // look for config in the home directory
		cfg.AddConfigPath(defaultConfigPath)                     // look for config in the home directory
		log.Printf("%s", defaultConfigPath)
		cfg.AddConfigPath(".")
	}

	var defaultConfig Config

	if err := cfg.ReadInConfig(); err != nil {
		// Create a default config file if it doesn't exist
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// set the default config file location

			defaultConfigPath = filepath.Join(defaultConfigPath, fmt.Sprintf(".%s.toml", ConfigFileName))

			cfg.SetConfigFile(defaultConfigPath)

			slog.Warn(fmt.Sprintf("Config file not found. Creating a default config file at %+v\n", cfg.ConfigFileUsed()))

			defaultConfig = getDefaultConfig()

			// log the default config
			slog.Debug(fmt.Sprintf("Default Config: %v", defaultConfig))

			// set the default values in viper
			setConfig(&defaultConfig, cfg)

			// print the contents of viper
			slog.Warn(fmt.Sprintf("Config: %v", cfg.AllSettings()))

			// Set the cache directory to the same directory as the config file
			defaultConfig.CacheDir = filepath.Join(filepath.Dir(cfg.ConfigFileUsed()), defaultConfig.CacheDir)

			// Create the directory if it doesn't exist
			if _, err := os.Stat(filepath.Dir(defaultConfig.CacheDir)); os.IsNotExist(err) {
				if err := os.MkdirAll(filepath.Dir(defaultConfig.CacheDir), 0755); err != nil {
					slog.Error(fmt.Sprintf("Error creating cache directory at %s", defaultConfig.CacheDir))
					return Config{}, err
				}
			}

			// marshal the default config to viper

			if err := cfg.WriteConfigAs(defaultConfigPath); err != nil {
				slog.Error(fmt.Sprintf("Error creating default config file at %s", defaultConfigPath))
				return Config{}, err
			} else {
				slog.Info(fmt.Sprintf("Default config file created at %s", defaultConfigPath))
			}
		} else {
			return Config{}, err
		}
	}

	//if defaultConfig.DBPass == "" {
	//	return Config{}, fmt.Errorf("DBPass environment variable is required in the config file")
	//}

	//encryptedPasswrd := defaultConfig.DBPass
	//decryptedPassword, err := security.Decrypt(encryptedPasswrd, encryptionKey)
	//if err != nil {
	//	return nil, err
	//}

	if err := cfg.Unmarshal(&defaultConfig); err != nil {
		return Config{}, err
	}

	return defaultConfig, nil
}

func (c *Config) SaveConfig(config *Config, filePath string) error {

	setConfig(config, c.cfg)
	if err := c.cfg.WriteConfig(); err != nil {
		return err
	}

	return nil
}

func (c *Config) GetConfig() Config {
	return *c
}

// HttpPort      int    `mapstructure:"http_port"`
// DBHost        string `mapstructure:"db_host"`
// DBPort        int    `mapstructure:"db_port"`
// DBName        string `mapstructure:"db_name"`
// DBUser        string `mapstructure:"db_user"`
// CacheDir      string `mapstructure:"cache_dir"`
// SessionSecret string `mapstructure:"session_secret"`
// Logger        Logger `mapstructure:"logger"`
func setConfig(config *Config, c *viper.Viper) {
	c.Set("db_host", config.DBHost)
	c.Set("http_port", config.HttpPort)
	c.Set("db_port", config.DBPort)
	c.Set("db_name", config.DBName)
	c.Set("db_user", config.DBUser)
	c.Set("cache_dir", config.CacheDir)
	c.Set("logger.style", config.Logger.Style)
	c.Set("logger.level", config.Logger.Level)
	c.Set("session_secret", config.SessionSecret)
}

// Returns the default configuration
func getDefaultConfig() Config {

	return Config{
		HttpPort: 9090,
		DBHost:   "file:///tmp/goflexpro.db",
		DBPort:   5432,
		DBName:   "goflexpro",
		Logger: Logger{
			Style: "text",
			Level: DebugLevelDebug,
		},
		SessionSecret: "vxwby2Qwtswp6z2z",
		CacheDir:      ".cache",
	}
}
