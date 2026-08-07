package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type model struct {
	screen screen
	focus  focus
	width  int
	height int

	setupInput textinput.Model
	composer   textarea.Model
	viewport   viewport.Model

	host         string
	session      string
	authLink     string
	config       Config
	stream       *streamClient
	menu         int
	notes        []Note
	selected     int
	hasMore      bool
	loadingOlder bool
	busy         bool
	status       string
	err          error
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
		m.hasMore = len(msg.notes) == requestLimit
		m.loadingOlder = false
		m.status = fmt.Sprintf("%d件", len(msg.notes))
		m.updateViewport()
		return m, m.ensureStream()
	case olderTimelineResult:
		m.loadingOlder = false
		if msg.err != nil {
			m.err, m.status = msg.err, "過去の投稿を読み込めませんでした"
			return m, nil
		}
		seen := make(map[string]struct{}, len(m.notes))
		for _, note := range m.notes {
			seen[note.ID] = struct{}{}
		}
		added := 0
		for _, note := range msg.notes {
			if _, ok := seen[note.ID]; ok {
				continue
			}
			m.notes = append(m.notes, note)
			seen[note.ID] = struct{}{}
			added++
		}
		m.hasMore = len(msg.notes) == requestLimit
		if added > 0 {
			m.selected = min(m.selected+1, len(m.notes)-1)
		}
		m.status = fmt.Sprintf("%d件", len(m.notes))
		m.updateViewport()
		return m, nil
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
		} else if len(m.notes) > 0 && m.hasMore && !m.busy && !m.loadingOlder {
			m.loadingOlder = true
			m.status = "過去の投稿を読み込み中…"
			return m, olderTimelineCmd(m.host, m.config.Token, m.menu, m.notes[len(m.notes)-1].ID)
		} else if !m.loadingOlder && !m.hasMore {
			m.status = "これ以上過去の投稿はありません"
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
	width := max(1, m.width-menuWidth-1)
	m.viewport.SetContent(m.renderNotes(width))
	if m.selected == 0 {
		m.viewport.GotoTop()
		return
	}
	selectedLine := m.selectedLineOffset(width)
	visibleLines := m.viewport.VisibleLineCount()
	if selectedLine < m.viewport.YOffset {
		m.viewport.SetYOffset(selectedLine)
		return
	}
	selectedHeight := lipgloss.Height(m.renderNote(m.selected, width))
	bottom := selectedLine + selectedHeight
	if bottom > m.viewport.YOffset+visibleLines {
		m.viewport.SetYOffset(bottom - visibleLines)
	}
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
