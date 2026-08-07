package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	appName        = "m2z"
	permission     = "read:account,write:notes"
	requestLimit   = 30
	menuWidth      = 18
	composerHeight = 3
)

type screen uint8

const (
	setupScreen screen = iota
	authScreen
	mainScreen
)

type focus uint8

const (
	menuFocus focus = iota
	contentFocus
	composerFocus
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Note struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Text      string `json:"text"`
	User      User   `json:"user"`
	Renote    *Note  `json:"renote"`
}

type Config struct {
	Host  string `json:"host"`
	Token string `json:"token"`
	User  User   `json:"user"`
}

type Meta struct {
	Name         string `json:"name"`
	URI          string `json:"uri"`
	SoftwareName string `json:"softwareName"`
}

type model struct {
	screen screen
	focus  focus
	width  int
	height int

	setupInput textinput.Model
	composer   textarea.Model
	viewport   viewport.Model

	host     string
	session  string
	authLink string
	config   Config
	stream   *streamClient
	menu     int
	notes    []Note
	selected int
	busy     bool
	status   string
	err      error
}

type metaResult struct {
	host string
	err  error
}

type authResult struct {
	token string
	user  User
	err   error
}

type timelineResult struct {
	notes []Note
	err   error
}

type postResult struct{ err error }
type browserResult struct{ err error }

var (
	accent        = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	dim           = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
)

func main() {
	cfg, err := loadConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "設定の読み込みに失敗しました:", err)
		os.Exit(1)
	}

	m := newModel(cfg)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newModel(cfg *Config) model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "https://misskey.example"
	input.CharLimit = 256
	input.Width = 60
	input.Focus()

	composer := textarea.New()
	composer.Placeholder = "投稿内容"
	composer.CharLimit = 3000
	composer.ShowLineNumbers = false
	composer.Blur()

	m := model{
		screen:     setupScreen,
		focus:      composerFocus,
		setupInput: input,
		composer:   composer,
		viewport:   viewport.New(1, 1),
		status:     "サーバーURLを入力してください",
	}
	if cfg != nil && cfg.Host != "" && cfg.Token != "" {
		m.screen = mainScreen
		m.focus = contentFocus
		m.host = cfg.Host
		m.config = *cfg
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.screen == mainScreen {
		return timelineCmd(m.host, m.config.Token, m.menu)
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case metaResult:
		m.busy = false
		if msg.err != nil {
			m.err, m.status = msg.err, "接続できませんでした"
			return m, nil
		}
		m.host = msg.host
		m.session = newSession()
		m.authLink = m.authURL()
		m.screen, m.status, m.err = authScreen, "ブラウザで認証してください", nil
		return m, openBrowserCmd(m.authLink)
	case browserResult:
		if msg.err != nil {
			m.err = msg.err
			m.status = "ブラウザを開けませんでした。URLを手動で開いてください"
		}
		return m, nil
	case authResult:
		m.busy = false
		if msg.err != nil {
			m.err, m.status = msg.err, "認証を確認できませんでした"
			return m, nil
		}
		m.config = Config{Host: m.host, Token: msg.token, User: msg.user}
		if err := saveConfig(m.config); err != nil {
			m.err, m.status = err, "設定を保存できませんでした"
			return m, nil
		}
		m.screen, m.focus, m.status, m.err = mainScreen, contentFocus, "認証しました。タイムラインを読み込み中…", nil
		return m, timelineCmd(m.host, m.config.Token, m.menu)
	case timelineResult:
		m.busy = false
		if msg.err != nil {
			m.err, m.status = msg.err, "タイムラインを読み込めませんでした"
			return m, nil
		}
		m.notes, m.selected, m.err = msg.notes, 0, nil
		m.status = fmt.Sprintf("%d件", len(msg.notes))
		m.updateViewport()
		return m, m.ensureStream()
	case streamNote, streamStatus, streamStopped:
		return m.streamMessage(msg)
	case postResult:
		m.busy = false
		if msg.err != nil {
			m.err, m.status = msg.err, "投稿に失敗しました"
			return m, nil
		}
		m.composer.Reset()
		m.status, m.err = "投稿しました", nil
		return m, timelineCmd(m.host, m.config.Token, m.menu)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" || (key == "q" && m.screen == mainScreen && m.focus != composerFocus) {
		m.stopStream()
		return m, tea.Quit
	}

	switch m.screen {
	case setupScreen:
		if key == "esc" {
			return m, tea.Quit
		}
		if key == "enter" && !m.busy {
			host := m.setupInput.Value()
			m.err, m.status, m.busy = nil, "接続を確認中…", true
			return m, checkHostCmd(host)
		}
		var cmd tea.Cmd
		m.setupInput, cmd = m.setupInput.Update(msg)
		return m, cmd
	case authScreen:
		if key == "esc" {
			m.screen, m.err, m.status = setupScreen, nil, "サーバーURLを入力してください"
			m.setupInput.Focus()
			return m, nil
		}
		if key == "o" && !m.busy {
			return m, openBrowserCmd(m.authLink)
		}
		if key == "enter" && !m.busy {
			m.busy, m.err, m.status = true, nil, "認証を確認中…"
			return m, checkAuthCmd(m.host, m.session)
		}
		return m, nil
	case mainScreen:
		return m.updateMainKey(msg)
	}
	return m, nil
}

func (m model) updateMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "tab" {
		m.focus = (m.focus + 1) % 3
		m.setFocus()
		return m, nil
	}
	if key == "shift+tab" {
		m.focus = (m.focus + 2) % 3
		m.setFocus()
		return m, nil
	}
	if key == "c" && m.focus != composerFocus {
		m.focus = composerFocus
		m.setFocus()
		return m, nil
	}
	if key == "r" && m.focus != composerFocus && !m.busy {
		m.busy, m.status, m.err = true, "更新中…", nil
		return m, timelineCmd(m.host, m.config.Token, m.menu)
	}
	if m.focus == composerFocus {
		if key == "enter" && !m.busy && strings.TrimSpace(m.composer.Value()) != "" {
			m.busy, m.status, m.err = true, "投稿中…", nil
			return m, postCmd(m.host, m.config.Token, m.composer.Value())
		}
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(msg)
		return m, cmd
	}
	if key == "j" || key == "down" {
		if m.focus == menuFocus {
			m.menu = (m.menu + 1) % 3
			m.status = ""
		} else if m.selected < len(m.notes)-1 {
			m.selected++
			m.updateViewport()
		}
		return m, nil
	}
	if key == "k" || key == "up" {
		if m.focus == menuFocus {
			m.menu = (m.menu + 2) % 3
			m.status = ""
		} else if m.selected > 0 {
			m.selected--
			m.updateViewport()
		}
		return m, nil
	}
	if key == "enter" && m.focus == menuFocus && !m.busy {
		m.stopStream()
		m.focus = contentFocus
		m.setFocus()
		m.busy, m.status = true, "読み込み中…"
		return m, timelineCmd(m.host, m.config.Token, m.menu)
	}
	if m.focus == contentFocus {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *model) setFocus() {
	if m.focus == composerFocus {
		m.composer.Focus()
	} else {
		m.composer.Blur()
	}
}

func (m *model) resize() {
	if m.width < 1 {
		return
	}
	contentWidth := max(1, m.width-menuWidth-1)
	contentHeight := max(1, m.height-7)
	m.viewport.Width, m.viewport.Height = contentWidth, contentHeight
	m.composer.SetWidth(max(1, m.width-4))
	m.composer.SetHeight(composerHeight)
	m.updateViewport()
}

func (m *model) updateViewport() {
	if m.screen != mainScreen || m.width < 1 {
		return
	}
	m.viewport.SetContent(m.renderNotes(max(1, m.width-menuWidth-1)))
	if m.selected == 0 {
		m.viewport.GotoTop()
	}
}

func (m model) renderNotes(width int) string {
	if len(m.notes) == 0 {
		return dim.Render("ノートがありません")
	}
	var out []string
	for i, note := range m.notes {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.selected {
			prefix = "▸ "
			style = selectedStyle
		}
		name := note.User.Name
		if name == "" {
			name = note.User.Username
		}
		handle := "@" + note.User.Username
		when := note.CreatedAt
		if t, err := time.Parse(time.RFC3339, note.CreatedAt); err == nil {
			when = t.Local().Format("01/02 15:04")
		}
		text := strings.TrimSpace(note.Text)
		if text == "" {
			text = "[本文なし]"
		}
		if note.Renote != nil {
			text = "↻ リノート\n" + strings.TrimSpace(note.Renote.Text)
		}
		block := fmt.Sprintf("%s%s %s  %s\n%s", prefix, name, handle, dim.Render(when), text)
		out = append(out, style.Width(max(1, width-2)).Padding(0, 1).Render(block))
	}
	divider := dim.Render(strings.Repeat("─", max(1, width-2)))
	return strings.Join(out, "\n"+divider+"\n")
}

func (m model) View() string {
	switch m.screen {
	case setupScreen:
		return m.setupView()
	case authScreen:
		return m.authView()
	default:
		return m.mainView()
	}
}

func (m model) setupView() string {
	body := strings.Join([]string{
		accent.Render("m2z — Misskey TUI"),
		"",
		"Misskeyサーバーを追加",
		"サーバーURL",
		m.setupInput.View(),
		"",
		m.statusLine(),
		"",
		dim.Render("Enter: 接続   Esc: 終了"),
	}, "\n")
	return lipgloss.NewStyle().Padding(2, 4).Render(body)
}

func (m model) authView() string {
	body := strings.Join([]string{
		accent.Render("m2z — Misskey認証"),
		"",
		m.host + " に接続",
		"",
		"ブラウザでログインとアクセス許可を完了してください。",
		"完了したらEnterで認証を確認します。",
		"",
		dim.Render("認証URL: " + m.authLink),
		"",
		m.statusLine(),
		"",
		dim.Render("o: ブラウザを開く   Enter: 認証確認   Esc: 戻る"),
	}, "\n")
	return lipgloss.NewStyle().Padding(2, 4).Render(body)
}

func (m model) mainView() string {
	if m.width < menuWidth+10 {
		return "画面幅が狭すぎます"
	}
	items := []string{"Home", "Local", "Global"}
	menuItems := []string{accent.Render("m2z"), ""}
	for i, item := range items {
		if i == m.menu {
			menuItems = append(menuItems, selectedStyle.Render("▸ "+item))
		} else {
			menuItems = append(menuItems, "  "+item)
		}
	}
	menuItems = append(menuItems, "", dim.Render(m.host))
	menu := lipgloss.NewStyle().Width(menuWidth).Render(strings.Join(menuItems, "\n"))

	name := m.config.User.Name
	if name == "" {
		name = m.config.User.Username
	}
	header := accent.Render(items[m.menu]) + "  " + name
	content := lipgloss.JoinVertical(lipgloss.Left, header, m.viewport.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, menu, lipgloss.NewStyle().Width(1).Render("│"), content)
	footer := lipgloss.NewStyle().BorderTop(true).Width(m.width).Render(m.composer.View() + "\n" + m.statusLine())
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

func (m model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render(m.status + ": " + m.err.Error())
	}
	return dim.Render(m.status)
}

func (m model) authURL() string {
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
		var meta Meta
		if err := apiCall(context.Background(), host+"/api/meta", "", map[string]any{}, &meta); err != nil {
			return metaResult{err: err}
		}
		return metaResult{host: host}
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

func timelineCmd(host, token string, kind int) tea.Cmd {
	return func() tea.Msg {
		path := []string{"/api/notes/timeline", "/api/notes/local-timeline", "/api/notes/global-timeline"}[min(kind, 2)]
		var notes []Note
		err := apiCall(context.Background(), host+path, token, map[string]any{"i": token, "limit": requestLimit}, &notes)
		return timelineResult{notes: notes, err: err}
	}
}

func postCmd(host, token, text string) tea.Cmd {
	return func() tea.Msg {
		err := apiCall(context.Background(), host+"/api/notes/create", token, map[string]any{"i": token, "text": text}, &struct{}{})
		return postResult{err: err}
	}
}

func openBrowserCmd(link string) tea.Cmd {
	return func() tea.Msg { return browserResult{err: openBrowser(link)} }
}

func apiCall(ctx context.Context, endpoint, token string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("レスポンスの解析に失敗: %w", err)
	}
	return nil
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
