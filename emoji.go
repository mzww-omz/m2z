package main

import (
	"math"
	"regexp"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	maxEmojiColumns = 8
	cellHeightRatio = 2.0
)

var customEmojiPattern = regexp.MustCompile(`:([A-Za-z0-9_+.-]+):`)

type CustomEmoji struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
	URL     string   `json:"url"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
}

type emojiCatalogResult struct {
	emojis []CustomEmoji
	err    error
}

func buildEmojiCatalog(emojis []CustomEmoji) map[string]CustomEmoji {
	catalog := make(map[string]CustomEmoji, len(emojis)*2)
	for _, emoji := range emojis {
		if emoji.Name == "" || emoji.URL == "" {
			continue
		}
		catalog[emoji.Name] = emoji
		for _, alias := range emoji.Aliases {
			if alias != "" {
				catalog[alias] = emoji
			}
		}
	}
	return catalog
}

func emojiDimensions(emoji CustomEmoji) (int, int) {
	width, height := emoji.Width, emoji.Height
	if width < 1 || height < 1 {
		return 2, 1
	}
	columns := int(math.Round(float64(width) / float64(height) * cellHeightRatio))
	return max(1, min(maxEmojiColumns, columns)), 1
}

func (m model) renderEmojiText(text string) string {
	if m.kitty == nil || !m.kitty.enabled || len(m.emojis) == 0 {
		return text
	}
	return customEmojiPattern.ReplaceAllStringFunc(text, func(token string) string {
		name := token[1 : len(token)-1]
		emoji, ok := m.emojis[name]
		if !ok {
			return token
		}
		columns, rows := emojiDimensions(emoji)
		return m.kitty.placeholderFor(emoji.URL, columns, rows)
	})
}

func (m *model) loadEmojiAssets(notes []Note) tea.Cmd {
	if m.kitty == nil || !m.kitty.enabled || len(m.emojis) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	seen := make(map[string]struct{})
	for _, note := range notes {
		for _, match := range customEmojiPattern.FindAllStringSubmatch(note.Text, -1) {
			if len(match) != 2 {
				continue
			}
			emoji, ok := m.emojis[match[1]]
			if !ok || emoji.URL == "" {
				continue
			}
			if _, ok := seen[emoji.URL]; ok {
				continue
			}
			seen[emoji.URL] = struct{}{}
			columns, rows := emojiDimensions(emoji)
			if cmd := m.kitty.prepareEmojiAsset(emoji.URL, columns, rows); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return batchCommands(cmds...)
}
