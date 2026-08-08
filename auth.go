package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type metaResult struct {
	host                string
	provider            Provider
	streamingURL        string
	statusMaxCharacters int
	err                 error
}

type mastodonAppResult struct {
	clientID     string
	clientSecret string
	err          error
}

type authResult struct {
	token string
	user  User
	err   error
}

type browserResult struct{ err error }

func (m model) authURL() string {
	if m.config.provider() == ProviderMastodon {
		return mastodonAuthURL(m.host, m.config.ClientID, m.pkceVerifier)
	}
	values := url.Values{}
	values.Set("name", appName)
	values.Set("permission", permission)
	return strings.TrimRight(m.host, "/") + "/miauth/" + m.session + "?" + values.Encode()
}

func checkHostCmd(raw string) tea.Cmd {
	return func() tea.Msg {
		host, err := normalizeHost(raw)
		if err != nil {
			return metaResult{err: err}
		}
		if instance, err := probeMastodon(context.Background(), host); err == nil {
			return metaResult{
				host:                host,
				provider:            ProviderMastodon,
				streamingURL:        instance.Configuration.URLs.Streaming,
				statusMaxCharacters: instance.Configuration.Statuses.MaxCharacters,
			}
		}
		var meta Meta
		if err := apiCall(context.Background(), host+"/api/meta", "", map[string]any{}, &meta); err != nil {
			return metaResult{err: err}
		}
		return metaResult{host: host, provider: ProviderMisskey}
	}
}

func checkAuthCmd(host, session string) tea.Cmd {
	return func() tea.Msg {
		var result struct {
			Token string `json:"token"`
			User  User   `json:"user"`
		}
		err := apiCall(context.Background(), host+"/api/miauth/"+session+"/check", "", map[string]any{}, &result)
		if err == nil && result.Token == "" {
			err = errors.New("認証が完了していません")
		}
		return authResult{token: result.Token, user: result.User, err: err}
	}
}

func openBrowserCmd(link string) tea.Cmd {
	return func() tea.Msg { return browserResult{err: openBrowser(link)} }
}

func normalizeHost(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("サーバーURLを入力してください")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "", errors.New("正しいサーバーURLを入力してください")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("httpまたはhttpsのURLを入力してください")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func newSession() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	h := hex.EncodeToString(b)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func openBrowser(link string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{link}
	case "linux":
		command, args = "xdg-open", []string{link}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", link}
	default:
		return errors.New("対応していないOSです")
	}
	return exec.Command(command, args...).Run()
}
