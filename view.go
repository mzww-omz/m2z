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
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
)

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
