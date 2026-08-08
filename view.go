package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const composerHeight = 3

var (
	accent        = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	dim           = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selectedStyle = lipgloss.NewStyle().Bold(true)
)

func (m model) View() string {
	switch m.screen {
	case setupScreen:
		return m.setupView()
	case authScreen:
		return m.authView()
	case settingsScreen:
		return m.settingsView()
	default:
		return m.mainView()
	}
}

func (m model) setupView() string {
	body := strings.Join([]string{
		accent.Render("m2z — ソーシャルサーバーTUI"),
		"",
		"サーバーを追加",
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
	lines := []string{
		accent.Render("m2z — 認証"),
		"",
		m.host + " に接続",
		"",
		"ブラウザでログインとアクセス許可を完了してください。",
	}
	if m.config.provider() == ProviderMastodon {
		lines = append(lines,
			"表示された認証コードを入力してください。",
			m.authInput.View(),
		)
	} else {
		lines = append(lines, "完了したらEnterで認証を確認します。")
	}
	lines = append(lines,
		"",
		dim.Render("認証URL: "+m.authLink),
		"",
		m.statusLine(),
		"",
		dim.Render("o: ブラウザを開く   Enter: 認証確認   Esc: 戻る"),
	)
	return lipgloss.NewStyle().Padding(2, 4).Render(strings.Join(lines, "\n"))
}

func (m model) settingsView() string {
	items := []string{"アイコンキャッシュを削除", "戻る"}
	lines := []string{accent.Render("m2z — 設定"), ""}
	if m.confirmReset {
		lines = append(lines,
			errorStyle.Render("アイコンキャッシュを削除しますか？"),
			"現在表示中のアイコンを再取得します。",
			"",
			dim.Render("y / Enter: 実行   n / Esc: キャンセル"),
		)
	} else {
		for i, item := range items {
			prefix := "  "
			if i == m.settingsIndex {
				prefix = "▸ "
				lines = append(lines, selectedStyle.Render(prefix+item))
			} else {
				lines = append(lines, prefix+item)
			}
		}
		lines = append(lines, "", m.statusLine(), "", dim.Render("j/k: 選択   Enter: 決定   Esc: 戻る"))
	}
	return lipgloss.NewStyle().Padding(2, 4).Render(strings.Join(lines, "\n"))
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
	menuItems = append(menuItems, "", dim.Render(m.host), "", dim.Render("s: 設定"))
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

func (m model) renderNotes(width int) string {
	if len(m.notes) == 0 {
		return dim.Render("投稿がありません")
	}
	blocks := make([]string, 0, len(m.notes))
	for i := range m.notes {
		blocks = append(blocks, m.renderNote(i, width))
	}
	divider := dim.Render(strings.Repeat("─", max(1, width-2)))
	return strings.Join(blocks, "\n"+divider+"\n")
}

func (m model) renderNote(index, width int) string {
	note := m.notes[index]
	prefix := "  "
	textStyle := lipgloss.NewStyle()
	if index == m.selected {
		prefix = "▸ "
		textStyle = selectedStyle
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
		label := note.ReshareLabel
		if label == "" {
			label = "リノート"
		}
		text = "↻ " + label + "\n" + strings.TrimSpace(note.Renote.Text)
	}
	text, emojiMarkers := m.layoutEmojiText(text)
	header := fmt.Sprintf("%s %s  %s", name, handle, dim.Render(when))
	details := fmt.Sprintf("%s\n%s", header, text)
	avatar := m.avatarPlaceholder(note.User.AvatarURL)
	if avatar == "" {
		rendered := textStyle.Width(max(1, width-2)).Padding(0, 1).Render(prefix + details)
		return replaceEmojiMarkers(rendered, emojiMarkers)
	}

	detailsWidth := max(1, width-2-lipgloss.Width(prefix)-kittyColumns-1)
	details = textStyle.Width(detailsWidth).Render(details)
	block := lipgloss.JoinHorizontal(lipgloss.Top, prefix, avatar, " ", details)
	rendered := lipgloss.NewStyle().Padding(0, 1).Render(block)
	return replaceEmojiMarkers(rendered, emojiMarkers)
}

func (m model) selectedLineOffset(width int) int {
	offset := 0
	dividerLines := 2
	for i := 0; i < m.selected && i < len(m.notes); i++ {
		offset += lipgloss.Height(m.renderNote(i, width)) + dividerLines
	}
	return offset
}
