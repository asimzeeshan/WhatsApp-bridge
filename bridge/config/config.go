package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Bridge    BridgeConfig    `toml:"bridge"`
	MCP       MCPConfig       `toml:"mcp"`
	Dashboard DashboardConfig `toml:"dashboard"`
}

type BridgeConfig struct {
	Addr            string              `toml:"addr"`
	DataDir         string              `toml:"data_dir"`
	LogLevel        string              `toml:"log_level"`
	CooldownMinutes int                 `toml:"cooldown_minutes"`
	Database        DatabaseConfig      `toml:"database"`
	RateLimit       RateLimitConfig     `toml:"ratelimit"`
	Media           MediaConfig         `toml:"media"`
	Monitoring      MonitorConfig       `toml:"monitoring"`
	Transcription   TranscriptionConfig `toml:"transcription"`
}

type DatabaseConfig struct {
	Driver string `toml:"driver"` // "sqlite" (default) or "postgres"
	DSN    string `toml:"dsn"`    // PostgreSQL connection string (only for postgres)
}

type RateLimitConfig struct {
	MessagesPerSecond float64 `toml:"messages_per_second"`
	Burst             int     `toml:"burst"`
	JitterMs          int     `toml:"jitter_ms"`
}

type MediaConfig struct {
	AutoDownloadImages bool   `toml:"auto_download_images"`
	ImageOutputDir     string `toml:"image_output_dir"`
	AutoDownloadAudio  bool   `toml:"auto_download_audio"`
	AudioOutputDir     string `toml:"audio_output_dir"`
	MaxFileSizeMB      int    `toml:"max_file_size_mb"`
	LogAllMedia        bool   `toml:"log_all_media"`
}

type TranscriptionConfig struct {
	Enabled    bool   `toml:"enabled"`
	WhisperURL string `toml:"whisper_url"`
	Model      string `toml:"model"`
	Language   string `toml:"language"`
}

type MonitorConfig struct {
	WatchedGroupJIDs []string `toml:"watched_group_jids"`
}

type MCPConfig struct {
	BridgeURL              string `toml:"bridge_url"`
	HealthCheckAttempts    int    `toml:"health_check_attempts"`
	HealthCheckDelaySeconds int   `toml:"health_check_delay_seconds"`
}

type DashboardConfig struct {
	Addr   string `toml:"addr"`
	DBPath string `toml:"db_path"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	setDefaults(cfg)
	return cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Bridge.Addr == "" {
		cfg.Bridge.Addr = "127.0.0.1:8080"
	}
	if cfg.Bridge.DataDir == "" {
		cfg.Bridge.DataDir = "./data"
	}
	if cfg.Bridge.LogLevel == "" {
		cfg.Bridge.LogLevel = "info"
	}
	if cfg.Bridge.CooldownMinutes == 0 {
		cfg.Bridge.CooldownMinutes = 10
	}
	if cfg.Bridge.RateLimit.MessagesPerSecond == 0 {
		cfg.Bridge.RateLimit.MessagesPerSecond = 0.5
	}
	if cfg.Bridge.RateLimit.Burst == 0 {
		cfg.Bridge.RateLimit.Burst = 3
	}
	if cfg.Bridge.RateLimit.JitterMs == 0 {
		cfg.Bridge.RateLimit.JitterMs = 500
	}
	if cfg.Bridge.Media.MaxFileSizeMB == 0 {
		cfg.Bridge.Media.MaxFileSizeMB = 50
	}
	if cfg.Bridge.Database.Driver == "" {
		cfg.Bridge.Database.Driver = "sqlite"
	}
}
