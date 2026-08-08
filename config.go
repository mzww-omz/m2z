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

type Account struct {
	Provider            Provider `json:"provider,omitempty"`
	Host                string   `json:"host"`
	Token               string   `json:"token"`
	User                User     `json:"user"`
	ClientID            string   `json:"clientId,omitempty"`
	ClientSecret        string   `json:"clientSecret,omitempty"`
	StreamingURL        string   `json:"streamingUrl,omitempty"`
	StatusMaxCharacters int      `json:"statusMaxCharacters,omitempty"`
}

type Config struct {
	Provider            Provider  `json:"provider,omitempty"`
	Host                string    `json:"host"`
	Token               string    `json:"token"`
	User                User      `json:"user"`
	ClientID            string    `json:"clientId,omitempty"`
	ClientSecret        string    `json:"clientSecret,omitempty"`
	StreamingURL        string    `json:"streamingUrl,omitempty"`
	StatusMaxCharacters int       `json:"statusMaxCharacters,omitempty"`
	Accounts            []Account `json:"accounts,omitempty"`
}

func (c Config) provider() Provider {
	if c.Provider == "" {
		return ProviderMisskey
	}
	return c.Provider
}

func (c Config) currentAccount() Account {
	return Account{
		Provider:            c.Provider,
		Host:                c.Host,
		Token:               c.Token,
		User:                c.User,
		ClientID:            c.ClientID,
		ClientSecret:        c.ClientSecret,
		StreamingURL:        c.StreamingURL,
		StatusMaxCharacters: c.StatusMaxCharacters,
	}
}

func (c *Config) setCurrentAccount(account Account) {
	c.Provider = account.Provider
	c.Host = account.Host
	c.Token = account.Token
	c.User = account.User
	c.ClientID = account.ClientID
	c.ClientSecret = account.ClientSecret
	c.StreamingURL = account.StreamingURL
	c.StatusMaxCharacters = account.StatusMaxCharacters
}

func (c *Config) rememberCurrentAccount() {
	current := c.currentAccount()
	for i, account := range c.Accounts {
		if sameAccount(account, current) {
			c.Accounts[i] = current
			return
		}
	}
	c.Accounts = append(c.Accounts, current)
}

func (c Config) accountKey() string {
	return accountKey(c.currentAccount())
}

func accountKey(account Account) string {
	provider := account.Provider
	if provider == "" {
		provider = ProviderMisskey
	}
	identity := account.User.ID
	if identity == "" {
		identity = account.User.Username
	}
	if identity == "" {
		identity = account.Token
	}
	return string(provider) + "\x00" + account.Host + "\x00" + identity
}

func sameAccount(a, b Account) bool {
	if a.Provider == "" {
		a.Provider = ProviderMisskey
	}
	if b.Provider == "" {
		b.Provider = ProviderMisskey
	}
	if a.Provider != b.Provider || a.Host != b.Host {
		return false
	}
	if a.User.ID != "" || b.User.ID != "" {
		return a.User.ID != "" && a.User.ID == b.User.ID
	}
	if a.User.Username != "" || b.User.Username != "" {
		return a.User.Username != "" && a.User.Username == b.User.Username
	}
	return a.Token != "" && a.Token == b.Token
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
	if cfg.Host == "" && len(cfg.Accounts) > 0 {
		cfg.setCurrentAccount(cfg.Accounts[0])
	}
	if cfg.Host != "" && cfg.Token != "" {
		cfg.rememberCurrentAccount()
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
