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

	setupInput    textinput.Model
	authInput     textinput.Model
	reactionInput textinput.Model
	composer      textarea.Model
	viewport      viewport.Model
	kitty         *kittyRenderer
	emojis        map[string]CustomEmoji

	host              string
	session           string
	authLink          string
	pkceVerifier      string
	config            Config
	stream            *streamClient
	menu              int
	settingsIndex     int
	confirmReset      bool
	notes             []Note
	selected          int
	replyTo           *Note
	hasMore           bool
	loadingOlder      bool
	busy              bool
	reactionMode      bool
	confirmRenote     bool
	refreshSelectedID string
	status            string
	err               error
}

func newModel(cfg *Config) model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "https://example.social"
	input.CharLimit = 256
	input.Width = 60
	input.Focus()

	authInput := textinput.New()
	authInput.Prompt = "> "
	authInput.Placeholder = "認証コード"
	authInput.CharLimit = 512
	authInput.Width = 60
	authInput.Blur()

	reactionInput := textinput.New()
	reactionInput.Prompt = "リアクション: "
	reactionInput.Placeholder = "👍 または :emoji:"
	reactionInput.CharLimit = 128
	reactionInput.Width = 40
	reactionInput.Blur()

	composer := textarea.New()
	composer.Placeholder = "投稿内容"
	composer.CharLimit = 3000
	composer.ShowLineNumbers = false
	composer.Blur()

	m := model{
		screen:        setupScreen,
		focus:         composerFocus,
		setupInput:    input,
		authInput:     authInput,
		reactionInput: reactionInput,
		composer:      composer,
		viewport:      viewport.New(1, 1),
		kitty:         newKittyRenderer(),
		emojis:        make(map[string]CustomEmoji),
		status:        "サーバーURLを入力してください",
	}
	if cfg != nil && cfg.Host != "" && cfg.Token != "" {
		m.screen = mainScreen
		m.focus = contentFocus
		m.host = cfg.Host
		m.config = *cfg
		if cfg.StatusMaxCharacters > 0 {
			m.composer.CharLimit = cfg.StatusMaxCharacters
		}
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.screen == mainScreen {
		return tea.Sequence(m.kitty.clearCmd(), batchCommands(timelineCmd(m.config, m.menu), emojiCatalogCmd(m.config)))
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
		m.config = Config{
			Provider:            msg.provider,
			Host:                msg.host,
			StreamingURL:        msg.streamingURL,
			StatusMaxCharacters: msg.statusMaxCharacters,
		}
		if msg.statusMaxCharacters > 0 {
			m.composer.CharLimit = msg.statusMaxCharacters
		}
		if msg.provider == ProviderMastodon {
			m.busy, m.status = true, "Mastodonアプリを登録中…"
			return m, registerMastodonAppCmd(m.host)
		}
		m.session = newSession()
		m.authLink = m.authURL()
		m.authInput.Blur()
		m.screen, m.status, m.err = authScreen, "ブラウザで認証してください", nil
		return m, openBrowserCmd(m.authLink)
	case mastodonAppResult:
		m.busy = false
		if msg.err != nil {
			m.err, m.status = msg.err, "Mastodonアプリを登録できませんでした"
			return m, nil
		}
		m.config.ClientID = msg.clientID
		m.config.ClientSecret = msg.clientSecret
		m.pkceVerifier = newPKCEVerifier()
		m.authLink = m.authURL()
		m.authInput.Reset()
		m.authInput.Focus()
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
		m.config.Host, m.config.Token, m.config.User = m.host, msg.token, msg.user
		m.authInput.Blur()
		if err := saveConfig(m.config); err != nil {
			m.err, m.status = err, "設定を保存できませんでした"
			return m, nil
		}
		m.screen, m.focus, m.status, m.err = mainScreen, contentFocus, "認証しました。タイムラインを読み込み中…", nil
		return m, tea.Sequence(m.kitty.clearCmd(), batchCommands(timelineCmd(m.config, m.menu), emojiCatalogCmd(m.config)))
	case emojiCatalogResult:
		if msg.err == nil {
			m.emojis = buildEmojiCatalog(msg.emojis)
			emojiCmd := m.loadEmojiAssets(m.notes)
			m.updateViewport()
			return m, emojiCmd
		}
		return m, nil
	case avatarResult:
		uploadCmd := m.kitty.finish(msg)
		m.updateViewport()
		return m, uploadCmd
	case timelineResult:
		m.busy = false
		selectedID := m.refreshSelectedID
		m.refreshSelectedID = ""
		if msg.err != nil {
			m.err, m.status = msg.err, "タイムラインを読み込めませんでした"
			return m, nil
		}
		m.notes, m.selected, m.err = msg.notes, 0, nil
		for i, note := range m.notes {
			if note.ID == selectedID {
				m.selected = i
				break
			}
		}
		m.hasMore = len(msg.notes) == requestLimit
		m.loadingOlder = false
		m.status = fmt.Sprintf("%d件", len(msg.notes))
		avatarCmd := m.loadAvatars(m.notes)
		emojiCmd := m.loadEmojiAssets(m.notes)
		m.updateViewport()
		return m, batchCommands(m.ensureStream(), avatarCmd, emojiCmd)
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
		avatarCmd := m.loadAvatars(msg.notes)
		emojiCmd := m.loadEmojiAssets(msg.notes)
		m.updateViewport()
		return m, batchCommands(avatarCmd, emojiCmd)
	case streamNote, streamStatus, streamStopped:
		return m.streamMessage(msg)
	case postResult:
		m.busy = false
		if msg.err != nil {
			m.err, m.status = msg.err, "投稿に失敗しました"
			return m, nil
		}
		m.composer.Reset()
		m.composer.Placeholder = "投稿内容"
		m.replyTo = nil
		m.resize()
		m.status, m.err = "投稿しました", nil
		return m, timelineCmd(m.config, m.menu)
	case reactionResult:
		if msg.err != nil {
			m.busy = false
			m.refreshSelectedID = ""
			m.err, m.status = msg.err, "リアクションに失敗しました"
			return m, nil
		}
		m.status, m.err = "リアクションしました。更新中…", nil
		return m, timelineCmd(m.config, m.menu)
	case renoteResult:
		if msg.err != nil {
			m.busy = false
			m.refreshSelectedID = ""
			m.err, m.status = msg.err, "リノートに失敗しました"
			return m, nil
		}
		m.status, m.err = "リノートしました。更新中…", nil
		return m, timelineCmd(m.config, m.menu)
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.screen != mainScreen || msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if msg.X < 0 || msg.X >= menuWidth || msg.Y < 2 || msg.Y > 4 {
		return m, nil
	}
	menu := msg.Y - 2
	if menu == m.menu {
		m.focus = contentFocus
		m.setFocus()
		return m, nil
	}
	m.stopStream()
	m.menu = menu
	m.focus = contentFocus
	m.setFocus()
	m.busy, m.status, m.err = true, "読み込み中…", nil
	return m, timelineCmd(m.config, m.menu)
}

func (m model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" || (key == "q" && (m.screen == mainScreen || m.screen == settingsScreen) && m.focus != composerFocus && !m.reactionMode && !m.confirmRenote) {
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
			m.authInput.Blur()
			m.setupInput.Focus()
			return m, nil
		}
		if key == "o" && !m.busy && m.config.provider() != ProviderMastodon {
			return m, openBrowserCmd(m.authLink)
		}
		if key == "enter" && !m.busy {
			m.busy, m.err, m.status = true, nil, "認証を確認中…"
			if m.config.provider() == ProviderMastodon {
				code := strings.TrimSpace(m.authInput.Value())
				if code == "" {
					m.busy, m.err, m.status = false, fmt.Errorf("認証コードを入力してください"), "認証を確認できませんでした"
					return m, nil
				}
				return m, mastodonTokenCmd(m.host, m.config.ClientID, m.config.ClientSecret, code, m.pkceVerifier)
			}
			return m, checkAuthCmd(m.host, m.session)
		}
		if m.config.provider() == ProviderMastodon {
			var cmd tea.Cmd
			m.authInput, cmd = m.authInput.Update(msg)
			return m, cmd
		}
		return m, nil
	case mainScreen:
		return m.updateMainKey(msg)
	case settingsScreen:
		return m.updateSettingsKey(msg)
	}
	return m, nil
}

func (m model) updateMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.reactionMode {
		if key == "esc" {
			m.reactionMode = false
			m.reactionInput.SetValue("")
			m.reactionInput.Blur()
			m.status = ""
			return m, nil
		}
		if key == "enter" {
			reaction := strings.TrimSpace(m.reactionInput.Value())
			if reaction == "" {
				m.status = "リアクションを入力してください"
				return m, nil
			}
			m.reactionMode = false
			m.reactionInput.Blur()
			m.busy, m.status, m.err = true, "リアクション中…", nil
			m.refreshSelectedID = m.selectedNoteID()
			return m, reactionCmd(m.host, m.config.Token, m.notes[m.selected], reaction)
		}
		var cmd tea.Cmd
		m.reactionInput, cmd = m.reactionInput.Update(msg)
		return m, cmd
	}
	if m.confirmRenote {
		switch key {
		case "y", "enter":
			m.confirmRenote = false
			m.busy, m.status, m.err = true, "リノート中…", nil
			m.refreshSelectedID = m.selectedNoteID()
			return m, renoteCmd(m.host, m.config.Token, m.notes[m.selected])
		case "n", "esc":
			m.confirmRenote = false
			m.status = ""
		}
		return m, nil
	}
	if key == "esc" && m.replyTo != nil {
		m.replyTo = nil
		m.composer.Reset()
		m.composer.Placeholder = "投稿内容"
		m.resize()
		m.focus = contentFocus
		m.setFocus()
		m.status = ""
		return m, nil
	}
	if key == "s" && m.focus != composerFocus {
		m.screen = settingsScreen
		m.settingsIndex = 0
		m.confirmReset = false
		return m, nil
	}
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
		m.replyTo = nil
		m.composer.Reset()
		m.composer.Placeholder = "投稿内容"
		m.resize()
		m.focus = composerFocus
		m.setFocus()
		return m, nil
	}
	if key == "a" && m.config.provider() == ProviderMisskey && m.focus == contentFocus && !m.busy && len(m.notes) > 0 {
		target := actionNote(m.notes[m.selected])
		value := "👍"
		if target.MyReaction != nil {
			value = *target.MyReaction
		}
		m.reactionInput.SetValue(value)
		m.reactionInput.Focus()
		m.reactionMode = true
		m.status = "リアクションを入力してください（Esc: キャンセル）"
		return m, nil
	}
	if key == "n" && m.config.provider() == ProviderMisskey && m.focus == contentFocus && !m.busy && len(m.notes) > 0 {
		m.confirmRenote = true
		m.status = "このノートをリノートしますか？ y/Enter: 実行 n/Esc: キャンセル"
		return m, nil
	}
	if key == "R" && m.focus != composerFocus && !m.busy && m.selected >= 0 && m.selected < len(m.notes) {
		target := m.notes[m.selected]
		m.replyTo = &target
		m.composer.Reset()
		m.composer.Placeholder = "返信内容"
		m.resize()
		m.focus = composerFocus
		m.setFocus()
		m.status, m.err = "返信内容を入力してください", nil
		return m, nil
	}
	if key == "r" && m.focus != composerFocus && !m.busy {
		m.busy, m.status, m.err = true, "更新中…", nil
		return m, timelineCmd(m.config, m.menu)
	}
	if m.focus == composerFocus {
		if key == "enter" && !m.busy && strings.TrimSpace(m.composer.Value()) != "" {
			m.busy, m.status, m.err = true, "投稿中…", nil
			replyID := ""
			if m.replyTo != nil {
				replyID = m.replyTo.ID
			}
			return m, postCmd(m.config, m.composer.Value(), replyID)
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
			return m, olderTimelineCmd(m.config, m.menu, m.notes[len(m.notes)-1].ID)
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
		return m, timelineCmd(m.config, m.menu)
	}
	if m.focus == contentFocus {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) updateSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.confirmReset {
		switch key {
		case "y", "enter":
			m.confirmReset = false
			m.status, m.err = "アイコンキャッシュを削除しました。再取得中…", nil
			return m, m.resetImageCache()
		case "n", "esc":
			m.confirmReset = false
		}
		return m, nil
	}
	if key == "esc" {
		m.screen, m.confirmReset = mainScreen, false
		return m, nil
	}
	if key == "j" || key == "down" {
		m.settingsIndex = (m.settingsIndex + 1) % 2
		return m, nil
	}
	if key == "k" || key == "up" {
		m.settingsIndex = (m.settingsIndex + 1) % 2
		return m, nil
	}
	if key == "enter" {
		if m.settingsIndex == 0 {
			m.confirmReset = true
			return m, nil
		}
		m.screen = mainScreen
	}
	return m, nil
}

func (m model) selectedNoteID() string {
	if m.selected < 0 || m.selected >= len(m.notes) {
		return ""
	}
	return m.notes[m.selected].ID
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
	if m.replyTo != nil {
		contentHeight = max(1, contentHeight-1)
	}
	m.viewport.Width, m.viewport.Height = contentWidth, contentHeight
	m.composer.SetWidth(max(1, m.width-4))
	m.composer.SetHeight(composerHeight)
	m.reactionInput.Width = max(1, m.width-4)
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
