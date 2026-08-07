package main

import (
	"math"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	maxEmojiColumns = 8
	cellHeightRatio = 2.0
)

var (
	customEmojiPattern    = regexp.MustCompile(`:([A-Za-z0-9_+.-]+):`)
	emojiMarkerGapPattern = regexp.MustCompile(`([\x{E000}-\x{F8FF}]+) +([\x{E000}-\x{F8FF}]+)`)
)

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

func (m model) layoutEmojiText(text string) (string, map[string]string) {
	if m.kitty == nil || !m.kitty.enabled || len(m.emojis) == 0 {
		return text, nil
	}
	matches := customEmojiPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	markers := make(map[string]string, len(matches))
	var out strings.Builder
	last := 0
	previousEmoji := false
	for index, match := range matches {
		start, end := match[0], match[1]
		segment := text[last:start]
		out.WriteString(segment)
		name := text[match[2]:match[3]]
		emoji, ok := m.emojis[name]
		if !ok {
			out.WriteString(text[start:end])
			previousEmoji = false
			last = end
			continue
		}
		if previousEmoji && segment == "" {
			out.WriteByte(' ')
		}
		columns, rows := m.emojiDisplaySize(emoji)
		marker := emojiMarker(index, columns)
		out.WriteString(marker)
		markers[marker] = m.kitty.placeholderFor(emoji.URL, columns, rows)
		previousEmoji = true
		last = end
	}
	out.WriteString(text[last:])
	return out.String(), markers
}

func (m model) emojiDisplaySize(emoji CustomEmoji) (int, int) {
	columns, rows := emojiDimensions(emoji)
	if image, ok := m.kitty.images[emoji.URL]; ok && image.ready {
		return image.columns, image.rows
	}
	return columns, rows
}

func emojiMarker(index, columns int) string {
	var marker strings.Builder
	for column := 0; column < columns; column++ {
		marker.WriteRune(rune(0xE000 + index*maxEmojiColumns + column))
	}
	return marker.String()
}

func replaceEmojiMarkers(text string, replacements map[string]string) string {
	text = emojiMarkerGapPattern.ReplaceAllString(text, `$1$2`)
	for marker, placeholder := range replacements {
		text = strings.ReplaceAll(text, marker, placeholder)
	}
	return text
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
