package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
			"https://example/avatar": {id: 42, ready: true},
		},
	}
	rendered := m.renderNote(0, 80)
	if strings.Index(rendered, "▸") >= strings.Index(rendered, string(rune(0x10EEEE))) {
		t.Fatalf("cursor is not before avatar: %q", rendered)
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

func TestKittyUploadAndPlaceholder(t *testing.T) {
	upload := kittyUpload([]byte("png"), 42)
	if !strings.Contains(upload, "a=T,U=1,f=100,i=42,c=4,r=2") {
		t.Fatalf("invalid Kitty upload sequence: %q", upload)
	}
	placeholder := kittyPlaceholder(42)
	if !strings.Contains(placeholder, string(rune(0x10EEEE))) ||
		!strings.Contains(placeholder, string(rune(0x030D))) ||
		strings.Count(placeholder, "\n") != kittyRows-1 ||
		strings.Count(placeholder, "\x1b[38;2;") != kittyRows {
		t.Fatalf("invalid Kitty placeholder size: %q", placeholder)
	}
}
