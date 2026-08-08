package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Provider string

const (
	ProviderMisskey  Provider = "misskey"
	ProviderMastodon Provider = "mastodon"
)

type Config struct {
	Provider            Provider `json:"provider,omitempty"`
	Host                string   `json:"host"`
	Token               string   `json:"token"`
	User                User     `json:"user"`
	ClientID            string   `json:"clientId,omitempty"`
	ClientSecret        string   `json:"clientSecret,omitempty"`
	StreamingURL        string   `json:"streamingUrl,omitempty"`
	StatusMaxCharacters int      `json:"statusMaxCharacters,omitempty"`
}

func (c Config) provider() Provider {
	if c.Provider == "" {
		return ProviderMisskey
	}
	return c.Provider
}

func configPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, appName, "config.json"), nil
}

func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func saveConfig(cfg Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
