package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

const composerHeight = 3

var (
	accent        = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	dim           = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	hashtagStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	renoteStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	selectedStyle = lipgloss.NewStyle().Bold(true)

	hashtagPattern = regexp.MustCompile(`(?m)(^|[^\p{L}\p{N}_])(#([\p{L}\p{N}_]+))`)
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
	title := "サーバーを追加"
	if m.addingAccount {
		title = "アカウントを追加"
	}
	body := strings.Join([]string{
		accent.Render("m2z — ソーシャルサーバーTUI"),
		"",
		title,
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

func (m model) settingsItems() []string {
	items := make([]string, 0, len(m.config.Accounts)+3)
	for _, account := range m.config.Accounts {
		name := account.User.Name
		if name == "" {
			name = account.User.Username
		}
		if name == "" {
			name = account.Host
		}
		if account.User.Username != "" && account.User.Username != name {
			name += " @" + account.User.Username
		}
		if sameAccount(account, m.config.currentAccount()) {
			name += " (現在)"
		}
		items = append(items, name)
	}
	return append(items, "アカウントを追加", "アイコンキャッシュを削除", "戻る")
}

func (m model) settingsView() string {
	items := m.settingsItems()
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
	composer := m.composer.View()
	if m.reactionMode {
		composer = m.reactionInput.View()
	}
	footerLines := []string{composer, m.statusLine()}
	if m.replyTo != nil {
		footerLines = append([]string{m.replyTargetView()}, footerLines...)
	}
	footer := lipgloss.NewStyle().BorderTop(true).Width(m.width).Render(strings.Join(footerLines, "\n"))
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

func (m model) replyTargetView() string {
	if m.replyTo == nil {
		return ""
	}
	if m.replyTo.User.Username != "" {
		return dim.Render("返信先: @" + m.replyTo.User.Username)
	}
	if m.replyTo.User.Name != "" {
		return dim.Render("返信先: " + m.replyTo.User.Name)
	}
	return dim.Render("返信先: " + m.replyTo.ID)
}

func (m model) statusLine() string {
	if m.err != nil {
		return errorStyle.Render(m.status + ": " + m.err.Error())
	}
	return dim.Render(m.status)
}

func styleHashtags(text string) string {
	return hashtagPattern.ReplaceAllStringFunc(text, func(match string) string {
		_, size := utf8.DecodeRuneInString(match)
		if match[0] == '#' {
			return hashtagStyle.Render(match)
		}
		return match[:size] + hashtagStyle.Render(match[size:])
	})
}

func (m model) renderAttachments(attachments []Attachment, width int) string {
	rows := make([]string, 0, len(attachments))
	row := make([]string, 0, len(attachments))
	rowWidth := 0
	width = max(1, width)
	flush := func() {
		if len(row) == 0 {
			return
		}
		parts := make([]string, 0, len(row)*2-1)
		for i, block := range row {
			if i > 0 {
				parts = append(parts, "  ")
			}
			parts = append(parts, block)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
		row = nil
		rowWidth = 0
	}

	for _, attachment := range attachments {
		if !attachment.isImage() {
			continue
		}
		block := ""
		if attachment.Sensitive {
			block = dim.Render("[センシティブ画像]")
		} else if m.kitty != nil && attachment.imageURL() != "" {
			block = m.kitty.placeholderFor(attachment.imageURL(), imageColumns, imageRows)
		}
		if block == "" {
			label := "[画像]"
			if attachment.Description != "" {
				label += " " + attachment.Description
			} else if attachment.URL != "" {
				label += " " + attachment.URL
			}
			block = dim.Render(label)
		}
		blockWidth := max(1, lipgloss.Width(block))
		if len(row) > 0 && rowWidth+2+blockWidth > width {
			flush()
		}
		row = append(row, block)
		if rowWidth > 0 {
			rowWidth += 2
		}
		rowWidth += blockWidth
	}
	flush()
	return strings.Join(rows, "\n")
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
	content := actionNote(note)
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
	text := strings.TrimSpace(content.Text)
	if text == "" {
		text = "[本文なし]"
	}
	if note.Renote != nil {
		label := note.ReshareLabel
		if label == "" {
			label = "リノート"
		}
		text = renoteStyle.Render("↻ "+label) + "\n" + text
	}
	if reactions := reactionSummary(content); reactions != "" {
		text += "\n" + reactions
	}
	text = styleHashtags(text)
	text, emojiMarkers := m.layoutEmojiText(text)
	header := fmt.Sprintf("%s %s  %s", name, handle, dim.Render(when))
	avatar := m.avatarPlaceholder(note.User.AvatarURL)
	detailsWidth := max(1, width-2)
	if avatar != "" {
		detailsWidth = max(1, width-2-lipgloss.Width(prefix)-kittyColumns-1)
	}
	details := fmt.Sprintf("%s\n%s", header, text)
	if attachments := m.renderAttachments(content.Attachments, detailsWidth); attachments != "" {
		details += "\n" + attachments
	}
	if avatar == "" {
		rendered := textStyle.Width(detailsWidth).Padding(0, 1).Render(prefix + details)
		return replaceEmojiMarkers(rendered, emojiMarkers)
	}

	details = textStyle.Width(detailsWidth).Render(details)
	block := lipgloss.JoinHorizontal(lipgloss.Top, prefix, avatar, " ", details)
	rendered := lipgloss.NewStyle().Padding(0, 1).Render(block)
	return replaceEmojiMarkers(rendered, emojiMarkers)
}

func reactionSummary(note Note) string {
	keys := make([]string, 0, len(note.Reactions)+1)
	for reaction := range note.Reactions {
		if reaction != "" {
			keys = append(keys, reaction)
		}
	}
	if note.MyReaction != nil && *note.MyReaction != "" {
		found := false
		for _, reaction := range keys {
			if reaction == *note.MyReaction {
				found = true
				break
			}
		}
		if !found {
			keys = append(keys, *note.MyReaction)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, reaction := range keys {
		label := reaction
		if note.MyReaction != nil && *note.MyReaction == reaction {
			label = accent.Render("★" + reaction)
		}
		parts = append(parts, fmt.Sprintf("%s %d", label, note.Reactions[reaction]))
	}
	return dim.Render(strings.Join(parts, "  "))
}

func (m model) selectedLineOffset(width int) int {
	offset := 0
	dividerLines := 2
	for i := 0; i < m.selected && i < len(m.notes); i++ {
		offset += lipgloss.Height(m.renderNote(i, width)) + dividerLines
	}
	return offset
}
