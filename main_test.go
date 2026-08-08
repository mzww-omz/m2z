package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestNoteReactionFields(t *testing.T) {
	var note Note
	if err := json.Unmarshal([]byte(`{"reactions":{"👍":2},"myReaction":"👍"}`), &note); err != nil {
		t.Fatal(err)
	}
	if note.Reactions["👍"] != 2 || note.MyReaction == nil || *note.MyReaction != "👍" {
		t.Fatalf("reaction fields not decoded: %+v", note)
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

func TestReactionAndRenoteCommandsUseTargetNote(t *testing.T) {
	var paths []string
	var payloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		paths = append(paths, r.URL.Path)
		payloads = append(payloads, payload)
		_, _ = w.Write([]byte(`{"createdNote":{}}`))
	}))
	defer server.Close()

	myReaction := "👍"
	note := Note{ID: "outer", Renote: &Note{ID: "inner", MyReaction: &myReaction}}
	if result := reactionCmd(server.URL, "token", note, "👍")().(reactionResult); result.err != nil {
		t.Fatal(result.err)
	}
	if result := reactionCmd(server.URL, "token", note, "🎉")().(reactionResult); result.err != nil {
		t.Fatal(result.err)
	}
	if result := renoteCmd(server.URL, "token", note)().(renoteResult); result.err != nil {
		t.Fatal(result.err)
	}

	if len(paths) != 3 || paths[0] != "/api/notes/reactions/delete" || paths[1] != "/api/notes/reactions/create" || paths[2] != "/api/notes/create" {
		t.Fatalf("unexpected API paths: %v", paths)
	}
	for i, payload := range payloads {
		if payload["noteId"] != "inner" && i < 2 {
			t.Fatalf("action %d targeted the wrong note: %#v", i, payload)
		}
	}
	if payloads[2]["renoteId"] != "inner" {
		t.Fatalf("renote targeted the wrong note: %#v", payloads[2])
	}
}

func TestReactionModeCanBeCancelled(t *testing.T) {
	m := newModel(&Config{Host: "https://misskey.example", Token: "token"})
	m.screen = mainScreen
	m.focus = contentFocus
	m.notes = []Note{{ID: "1"}}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil || !updated.(model).reactionMode {
		t.Fatal("reaction mode did not open")
	}
	updated, cmd = updated.(model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(model)
	if cmd != nil || got.reactionMode || got.reactionInput.Value() != "" {
		t.Fatalf("reaction mode did not cancel: %+v", got)
	}
}

func TestTimelineRefreshPreservesSelectedNote(t *testing.T) {
	m := newModel(nil)
	m.screen = mainScreen
	m.notes = []Note{{ID: "old"}, {ID: "selected"}}
	m.selected = 1
	m.refreshSelectedID = "selected"

	updated, _ := m.Update(timelineResult{notes: []Note{{ID: "new"}, {ID: "selected"}}})
	got := updated.(model)
	if got.selected != 1 || got.notes[got.selected].ID != "selected" {
		t.Fatalf("selected note was not preserved: selected=%d notes=%+v", got.selected, got.notes)
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

func TestRenderNoteHighlightsHashtagsRenotesAndReactions(t *testing.T) {
	m := newModel(nil)
	myReaction := "👍"
	m.notes = []Note{{
		ID:   "1",
		Text: "本文",
		Renote: &Note{
			Text:       "再投稿 #タグ",
			Reactions:  map[string]int{"👍": 2},
			MyReaction: &myReaction,
		},
	}}

	rendered := m.renderNote(0, 80)
	for _, want := range []string{
		renoteStyle.Render("↻ リノート"),
		hashtagStyle.Render("#タグ"),
		"★👍",
		"2",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered note does not contain styled %q: %q", want, rendered)
		}
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

func TestAvatarUploadBypassesBufferedView(t *testing.T) {
	const avatarURL = "https://example/avatar"
	var output bytes.Buffer
	m := newModel(nil)
	m.screen = mainScreen
	m.width, m.height = 80, 24
	m.notes = []Note{{ID: "1", User: User{Name: "user", Username: "user", AvatarURL: avatarURL}}}
	m.kitty = &kittyRenderer{
		enabled: true,
		output:  &output,
		images:  map[string]*kittyImage{avatarURL: {id: 42, placementID: 42, columns: kittyColumns, rows: kittyRows, loading: true}},
	}
	m.resize()

	updated, cmd := m.Update(avatarResult{url: avatarURL, data: []byte("png")})
	if cmd == nil {
		t.Fatal("avatar upload command is missing")
	}
	if strings.Contains(updated.(model).View(), "\x1b_G") {
		t.Fatal("avatar upload leaked into the buffered view")
	}
	cmd()
	if !strings.Contains(output.String(), "a=T,U=1,f=100,i=42") {
		t.Fatalf("avatar was not written directly: %q", output.String())
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
	virtualUpload := kittyUploadMode([]byte("png"), 42, 2, 1, true, 42)
	if !strings.Contains(virtualUpload, "a=t,t=d,f=100,i=42,p=42,q=2") ||
		!strings.Contains(virtualUpload, "a=p,U=1,i=42,c=2,r=1,p=42,q=2") {
		t.Fatalf("invalid virtual placement sequence: %q", virtualUpload)
	}
	placementPlaceholder := kittyPlaceholderWithPlacement(42, 2, 1, 42)
	if !strings.Contains(placementPlaceholder, "\x1b[58;2;0;0;42m") || !strings.Contains(placementPlaceholder, "\x1b[59m") {
		t.Fatalf("missing Kitty placement id: %q", placementPlaceholder)
	}
	placeholder := kittyPlaceholder(42, 4, 2)
	if !strings.Contains(placeholder, string(rune(0x10EEEE))) ||
		!strings.Contains(placeholder, string(rune(0x030D))) ||
		strings.Count(placeholder, "\n") != kittyRows-1 ||
		strings.Count(placeholder, "\x1b[38;2;") != kittyRows {
		t.Fatalf("invalid Kitty placeholder size: %q", placeholder)
	}
}
