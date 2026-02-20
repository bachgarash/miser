package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Proxy    ProxyConfig            `toml:"proxy"`
	Models   map[string]ModelConfig `toml:"models"`
	Fallback *PricingConfig         `toml:"fallback"`
}

type ProxyConfig struct {
	Port    int    `toml:"port"`
	Target  string `toml:"target"`
	Timeout string `toml:"timeout"`
}

type ModelConfig struct {
	Aliases           []string `toml:"aliases"`
	InputPerMTok      float64  `toml:"input_per_mtok"`
	OutputPerMTok     float64  `toml:"output_per_mtok"`
	CacheReadPerMTok  float64  `toml:"cache_read_per_mtok"`
	CacheWritePerMTok float64  `toml:"cache_write_per_mtok"`
}

type PricingConfig struct {
	InputPerMTok      float64 `toml:"input_per_mtok"`
	OutputPerMTok     float64 `toml:"output_per_mtok"`
	CacheReadPerMTok  float64 `toml:"cache_read_per_mtok"`
	CacheWritePerMTok float64 `toml:"cache_write_per_mtok"`
}

func Default() Config {
	return Config{
		Proxy: ProxyConfig{
			Port:    8080,
			Target:  "https://api.anthropic.com",
			Timeout: "5m",
		},
	}
}

// Load reads config from the given path. If path is empty, it searches
// ./miser.toml then ~/.config/miser/config.toml. If no file is found,
// it returns defaults without error.
func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		path = discover()
	}
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config %s: %w", path, err)
	}

	if cfg.Proxy.Port == 0 {
		cfg.Proxy.Port = 8080
	}
	if cfg.Proxy.Target == "" {
		cfg.Proxy.Target = "https://api.anthropic.com"
	}
	if cfg.Proxy.Timeout == "" {
		cfg.Proxy.Timeout = "5m"
	}

	return cfg, nil
}

func (c *Config) ProxyTimeout() time.Duration {
	d, err := time.ParseDuration(c.Proxy.Timeout)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

func discover() string {
	if _, err := os.Stat("miser.toml"); err == nil {
		return "miser.toml"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	xdg := filepath.Join(home, ".config", "miser", "config.toml")
	if _, err := os.Stat(xdg); err == nil {
		return xdg
	}

	return ""
}
