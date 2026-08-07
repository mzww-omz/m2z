package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"misskey.example":          "https://misskey.example",
		"https://misskey.example/": "https://misskey.example",
		"http://localhost:3000/":   "http://localhost:3000",
	}
	for input, want := range cases {
		got, err := normalizeHost(input)
		if err != nil || got != want {
			t.Errorf("normalizeHost(%q) = %q, %v; want %q", input, got, err, want)
		}
	}

	for _, input := range []string{"", "ftp://misskey.example", "https://user:pass@misskey.example"} {
		if _, err := normalizeHost(input); err == nil {
			t.Errorf("normalizeHost(%q) accepted invalid URL", input)
		}
	}
}

func TestStreamingURL(t *testing.T) {
	got, err := streamingURL("https://misskey.example/base", "token")
	if err != nil || got != "wss://misskey.example/base/streaming?i=token" {
		t.Fatalf("streamingURL() = %q, %v", got, err)
	}
	if channelName(0) != "homeTimeline" || channelName(1) != "localTimeline" || channelName(2) != "globalTimeline" {
		t.Fatal("unexpected timeline channel mapping")
	}
}

func TestNoteEmojiRefsAcceptObjectAndArray(t *testing.T) {
	var object Note
	if err := json.Unmarshal([]byte(`{"emojis":{"wide":"https://example/wide.png"}}`), &object); err != nil {
		t.Fatal(err)
	}
	if object.Emojis["wide"] != "https://example/wide.png" {
		t.Fatalf("object emoji refs not decoded: %#v", object.Emojis)
	}

	var array Note
	if err := json.Unmarshal([]byte(`{"emojis":["smile"]}`), &array); err != nil {
		t.Fatal(err)
	}
	if _, ok := array.Emojis["smile"]; !ok {
		t.Fatalf("array emoji refs not decoded: %#v", array.Emojis)
	}
}

func TestOlderTimelineAppendsNotes(t *testing.T) {
	m := newModel(&Config{Host: "https://misskey.example", Token: "token"})
	m.screen = mainScreen
	m.notes = []Note{{ID: "new"}}
	m.selected = 0
	m.hasMore = true

	updated, _ := m.Update(olderTimelineResult{
		notes: []Note{{ID: "old"}},
	})
	got := updated.(model)
	if len(got.notes) != 2 || got.notes[1].ID != "old" || got.selected != 1 {
		t.Fatalf("older notes were not appended: %+v", got.notes)
	}
}

func TestMouseSelectsTimelineMenu(t *testing.T) {
	m := newModel(&Config{Host: "https://misskey.example", Token: "token"})
	m.screen = mainScreen
	m.menu = 0

	updated, cmd := m.Update(tea.MouseMsg{
		X:      2,
		Y:      3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	got := updated.(model)
	if got.menu != 1 || !got.busy || cmd == nil {
		t.Fatalf("mouse did not select Local: menu=%d busy=%v cmd=%v", got.menu, got.busy, cmd != nil)
	}
}

func TestSelectedCursorAppearsBeforeAvatar(t *testing.T) {
	m := newModel(nil)
	m.notes = []Note{{ID: "1", User: User{Name: "user", Username: "user", AvatarURL: "https://example/avatar"}}}
	m.selected = 0
	m.kitty = &kittyRenderer{
		enabled: true,
		images: map[string]*kittyImage{
			"https://example/avatar": {id: 42, columns: kittyColumns, rows: kittyRows, ready: true},
		},
	}
	rendered := m.renderNote(0, 80)
	if strings.Index(rendered, "▸") >= strings.Index(rendered, string(rune(0x10EEEE))) {
		t.Fatalf("cursor is not before avatar: %q", rendered)
	}
}

func TestAvatarResultRefreshesViewport(t *testing.T) {
	const avatarURL = "https://example/avatar"
	m := newModel(nil)
	m.screen = mainScreen
	m.width, m.height = 80, 24
	m.notes = []Note{{ID: "1", User: User{Name: "user", Username: "user", AvatarURL: avatarURL}}}
	m.kitty = &kittyRenderer{
		enabled: true,
		images:  map[string]*kittyImage{avatarURL: {id: 42, columns: kittyColumns, rows: kittyRows, loading: true}},
	}
	m.resize()

	updated, _ := m.Update(avatarResult{url: avatarURL, data: []byte("png")})
	got := updated.(model)
	if !strings.Contains(got.viewport.View(), string(rune(0x10EEEE))) {
		t.Fatal("viewport was not refreshed after avatar load")
	}
}

func TestMissingAvatarPlaceholder(t *testing.T) {
	m := newModel(nil)
	m.notes = []Note{{ID: "1", User: User{Name: "user", Username: "user"}}}
	m.kitty = &kittyRenderer{enabled: true, images: map[string]*kittyImage{}}
	if !strings.Contains(m.renderNote(0, 80), "·") {
		t.Fatal("missing avatar placeholder is missing")
	}
}

func TestLoadingAvatarPlaceholder(t *testing.T) {
	m := newModel(nil)
	m.notes = []Note{{ID: "1", User: User{Name: "user", Username: "user", AvatarURL: "https://example/avatar"}}}
	m.kitty = &kittyRenderer{
		enabled: true,
		images: map[string]*kittyImage{
			"https://example/avatar": {id: 42, loading: true},
		},
	}
	if !strings.Contains(m.renderNote(0, 80), "◌") {
		t.Fatal("loading avatar placeholder is missing")
	}
}

func TestDownloadedEmojiSizeOverridesCatalogFallback(t *testing.T) {
	const imageURL = "https://example/wide"
	k := &kittyRenderer{images: map[string]*kittyImage{
		imageURL: {id: 1, columns: 2, rows: 1, autoSize: true, loading: true},
	}}
	k.finish(avatarResult{url: imageURL, data: []byte("png"), width: 400, height: 100})
	if k.images[imageURL].columns != maxEmojiColumns {
		t.Fatalf("wide image size was not applied: %d", k.images[imageURL].columns)
	}
}

func TestEmojiDimensionsPreserveWideImages(t *testing.T) {
	if columns, rows := emojiDimensions(CustomEmoji{Width: 1, Height: 1}); columns != 2 || rows != 1 {
		t.Fatalf("square emoji size = %dx%d", columns, rows)
	}
	if columns, rows := emojiDimensions(CustomEmoji{Width: 4, Height: 1}); columns != 8 || rows != 1 {
		t.Fatalf("wide emoji size = %dx%d", columns, rows)
	}
	if columns, _ := emojiDimensions(CustomEmoji{Width: 20, Height: 1}); columns != maxEmojiColumns {
		t.Fatalf("wide emoji was not capped: %d", columns)
	}
}

func TestAdjacentEmojiPlaceholdersWrapAsUnits(t *testing.T) {
	m := newModel(nil)
	m.kitty = &kittyRenderer{
		enabled: true,
		images: map[string]*kittyImage{
			"https://example/a": {id: 1, columns: 4, rows: 1, ready: true},
			"https://example/b": {id: 2, columns: 4, rows: 1, ready: true},
		},
	}
	m.emojis = map[string]CustomEmoji{
		"a": {Name: "a", URL: "https://example/a"},
		"b": {Name: "b", URL: "https://example/b"},
	}
	layout, markers := m.layoutEmojiText(":a::b:")
	rendered := replaceEmojiMarkers(lipgloss.NewStyle().Width(7).Render(layout), markers)
	if strings.Count(rendered, string(rune(0x10EEEE))) != 8 || strings.Count(rendered, "\x1b[39m") != 2 || strings.Contains(rendered, "\x1b[39m \x1b[38;") {
		t.Fatalf("adjacent emoji placeholders were split: %q", rendered)
	}
}

func TestKittyUploadAndPlaceholder(t *testing.T) {
	upload := kittyUpload([]byte("png"), 42, 4, 2)
	if !strings.Contains(upload, "a=T,U=1,f=100,i=42,c=4,r=2") {
		t.Fatalf("invalid Kitty upload sequence: %q", upload)
	}
	fitUpload := kittyUploadMode([]byte("png"), 42, 8, 1, true)
	if !strings.Contains(fitUpload, "a=T,U=1,f=100,i=42,c=8,q=2") || strings.Contains(fitUpload, "i=42,c=8,r=") {
		t.Fatalf("custom emoji upload did not preserve aspect ratio: %q", fitUpload)
	}
	placeholder := kittyPlaceholder(42, 4, 2)
	if !strings.Contains(placeholder, string(rune(0x10EEEE))) ||
		!strings.Contains(placeholder, string(rune(0x030D))) ||
		strings.Count(placeholder, "\n") != kittyRows-1 ||
		strings.Count(placeholder, "\x1b[38;2;") != kittyRows {
		t.Fatalf("invalid Kitty placeholder size: %q", placeholder)
	}
}
