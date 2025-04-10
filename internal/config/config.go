package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"runtime"
	"time"
)

// Config holds all configuration settings for the Dragonscale system.
type Config struct {
	System     SystemConfig     `json:"system"`
	WorkerPool WorkerPoolConfig `json:"workerPool"`
	Scheduler  SchedulerConfig  `json:"scheduler"`
	ToolsConfig ToolsConfig     `json:"tools"`
	EventBus   EventBusConfig   `json:"eventBus"`
}

// SystemConfig holds general system settings.
type SystemConfig struct {
	LogLevel        string `json:"logLevel"`        // debug, info, warn, error
	MetricsEnabled  bool   `json:"metricsEnabled"`  // Enable metrics collection
	MetricsEndpoint string `json:"metricsEndpoint"` // Where to send metrics
}

// WorkerPoolConfig holds settings for the worker pool.
type WorkerPoolConfig struct {
	InitialWorkers int     `json:"initialWorkers"` // Initial number of workers
	MinWorkers     int     `json:"minWorkers"`     // Minimum number of workers
	MaxWorkers     int     `json:"maxWorkers"`     // Maximum number of workers
	QueueSize      int     `json:"queueSize"`      // Size of the task queue
	CPUThreshold   float64 `json:"cpuThreshold"`   // CPU usage threshold for scaling
	MemThreshold   float64 `json:"memThreshold"`   // Memory usage threshold for scaling
}

// SchedulerConfig holds settings for the task scheduler.
type SchedulerConfig struct {
	EnablePriorityBoost bool          `json:"enablePriorityBoost"` // Boost priority of long-waiting tasks
	MaxTaskRetries      int           `json:"maxTaskRetries"`      // Maximum retries for failed tasks
	DefaultInterval     time.Duration `json:"defaultInterval"`     // Default interval for recurring tasks
}

// ToolsConfig holds settings for the tool manager.
type ToolsConfig struct {
	DefaultTimeout int      `json:"defaultTimeout"` // Default timeout in seconds
	AllowedTools   []string `json:"allowedTools"`   // List of allowed tools
	SandboxEnabled bool     `json:"sandboxEnabled"` // Enable tool sandboxing
}

// EventBusConfig holds settings for the event bus.
type EventBusConfig struct {
	DefaultBufferSize int `json:"defaultBufferSize"` // Default buffer size for subscribers
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		System: SystemConfig{
			LogLevel:        "info",
			MetricsEnabled:  false,
			MetricsEndpoint: "http://localhost:9091/metrics",
		},
		WorkerPool: WorkerPoolConfig{
			InitialWorkers: runtime.NumCPU(),
			MinWorkers:     1,
			MaxWorkers:     runtime.NumCPU() * 4,
			QueueSize:      100,
			CPUThreshold:   0.8,
			MemThreshold:   0.9,
		},
		Scheduler: SchedulerConfig{
			EnablePriorityBoost: true,
			MaxTaskRetries:      3,
			DefaultInterval:     30 * time.Second,
		},
		ToolsConfig: ToolsConfig{
			DefaultTimeout: 10,
			AllowedTools:   []string{"*"}, // Allow all by default
			SandboxEnabled: true,
		},
		EventBus: EventBusConfig{
			DefaultBufferSize: 10,
		},
	}
}

// LoadFromFile loads configuration from a JSON file.
func LoadFromFile(filePath string) (*Config, error) {
	// Start with default config
	config := DefaultConfig()

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("Config file %s not found, using defaults", filePath)
		return config, nil
	}

	// Read file
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Parse JSON
	err = json.Unmarshal(data, config)
	if err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	log.Printf("Loaded configuration from %s", filePath)
	return config, nil
}

// SaveToFile saves the configuration to a JSON file.
func (c *Config) SaveToFile(filePath string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("error marshaling config: %w", err)
	}

	err = ioutil.WriteFile(filePath, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing config file: %w", err)
	}

	log.Printf("Saved configuration to %s", filePath)
	return nil
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	// Validate worker pool config
	if c.WorkerPool.MinWorkers < 1 {
		return fmt.Errorf("minWorkers must be at least 1")
	}
	if c.WorkerPool.MaxWorkers < c.WorkerPool.MinWorkers {
		return fmt.Errorf("maxWorkers must be greater than or equal to minWorkers")
	}
	if c.WorkerPool.InitialWorkers < c.WorkerPool.MinWorkers || c.WorkerPool.InitialWorkers > c.WorkerPool.MaxWorkers {
		return fmt.Errorf("initialWorkers must be between minWorkers and maxWorkers")
	}
	if c.WorkerPool.QueueSize < 1 {
		return fmt.Errorf("queueSize must be at least 1")
	}

	// Validate scheduler config
	if c.Scheduler.MaxTaskRetries < 0 {
		return fmt.Errorf("maxTaskRetries cannot be negative")
	}

	// Validate tools config
	if c.ToolsConfig.DefaultTimeout < 1 {
		return fmt.Errorf("defaultTimeout must be at least 1 second")
	}

	// Validate event bus config
	if c.EventBus.DefaultBufferSize < 1 {
		return fmt.Errorf("defaultBufferSize must be at least 1")
	}

	return nil
}
