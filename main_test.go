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
	if err := json.Unmarshal([]byte(`{"reactions":{"👍":2},"reactionEmojis":{"emoji@fedibird.com":"https://example/emoji.png"},"myReaction":"👍"}`), &note); err != nil {
		t.Fatal(err)
	}
	if note.Reactions["👍"] != 2 || note.ReactionEmojis["emoji@fedibird.com"] != "https://example/emoji.png" || note.MyReaction == nil || *note.MyReaction != "👍" {
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

func TestNoteAttachmentsDecodeMisskeyFiles(t *testing.T) {
	var note Note
	if err := json.Unmarshal([]byte(`{"files":[{"url":"https://example/image.png","thumbnailUrl":"https://example/thumb.png","type":"image/png","comment":"説明"},{"url":"https://example/video.mp4","type":"video/mp4"}]}`), &note); err != nil {
		t.Fatal(err)
	}
	if len(note.Attachments) != 2 || !note.Attachments[0].isImage() || note.Attachments[0].imageURL() != "https://example/thumb.png" {
		t.Fatalf("image attachment was not decoded: %+v", note.Attachments)
	}
	if note.Attachments[0].Description != "説明" || note.Attachments[1].isImage() {
		t.Fatalf("attachment metadata was not preserved: %+v", note.Attachments)
	}
}

func TestNoteContentWarningDecodes(t *testing.T) {
	var note Note
	if err := json.Unmarshal([]byte(`{"id":"1","cw":"spoiler","text":"secret"}`), &note); err != nil {
		t.Fatal(err)
	}
	if contentWarning(note) != "spoiler" {
		t.Fatalf("content warning was not decoded: %q", note.ContentWarning)
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

func TestPostCmdPayload(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replyID string
		wantID  bool
	}{
		{name: "reply", replyID: "note-id", wantID: true},
		{name: "post", wantID: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var payload map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode request: %v", err)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			result := postCmd(Config{Host: server.URL, Token: "token"}, "本文", tc.replyID)().(postResult)
			if result.err != nil {
				t.Fatal(result.err)
			}
			_, hasReplyID := payload["replyId"]
			if hasReplyID != tc.wantID {
				t.Fatalf("replyId presence = %v, want %v: %#v", hasReplyID, tc.wantID, payload)
			}
			if tc.wantID && payload["replyId"] != tc.replyID {
				t.Fatalf("replyId = %v, want %q", payload["replyId"], tc.replyID)
			}
		})
	}
}

func TestReplyKeySelectsAndCancelsNote(t *testing.T) {
	m := newModel(nil)
	m.screen = mainScreen
	m.focus = contentFocus
	m.notes = []Note{{ID: "note-id", User: User{Username: "alice"}}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	got := updated.(model)
	if got.replyTo == nil || got.replyTo.ID != "note-id" || got.focus != composerFocus {
		t.Fatalf("reply mode was not entered: replyTo=%+v focus=%v", got.replyTo, got.focus)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(model)
	if got.replyTo != nil || got.focus != contentFocus {
		t.Fatalf("reply mode was not cancelled: replyTo=%+v focus=%v", got.replyTo, got.focus)
	}
}

func TestPostResultClearsReplyMode(t *testing.T) {
	m := newModel(&Config{Host: "https://misskey.example", Token: "token"})
	m.screen = mainScreen
	m.width, m.height = 80, 24
	m.replyTo = &Note{ID: "note-id"}
	m.composer.SetValue("返信")
	m.busy = true

	updated, cmd := m.Update(postResult{})
	got := updated.(model)
	if got.replyTo != nil || got.composer.Value() != "" || got.composer.Placeholder != "投稿内容" || cmd == nil {
		t.Fatalf("reply mode was not cleared after posting: replyTo=%+v text=%q placeholder=%q cmd=%v", got.replyTo, got.composer.Value(), got.composer.Placeholder, cmd != nil)
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

func TestRenderNoteDisplaysReactionEmojiRef(t *testing.T) {
	const emojiURL = "https://example.social/party.png"
	m := newModel(nil)
	m.kitty = &kittyRenderer{
		enabled: true,
		images: map[string]*kittyImage{
			emojiURL: {id: 1, placementID: 1, columns: 2, rows: 1, ready: true},
		},
	}
	m.notes = []Note{{
		Text:           "本文",
		ReactionEmojis: EmojiRefs{"party@example.social": emojiURL},
		Reactions:      map[string]int{":party@example.social:": 2},
	}}

	rendered := m.renderNote(0, 80)
	if strings.Contains(rendered, "party@example.social") || !strings.Contains(rendered, string(rune(0x10EEEE))) || !strings.Contains(rendered, "2") {
		t.Fatalf("note-local reaction emoji was not rendered: %q", rendered)
	}
}

func TestLoadEmojiAssetsUsesReactionEmojiRefsWithoutCatalog(t *testing.T) {
	const emojiURL = "https://example.social/party.png"
	m := newModel(nil)
	m.kitty = &kittyRenderer{enabled: true, images: make(map[string]*kittyImage)}
	note := Note{
		ReactionEmojis: EmojiRefs{"party@example.social": emojiURL},
		Reactions:      map[string]int{":party@example.social:": 2},
	}
	if cmd := m.loadEmojiAssets([]Note{note}); cmd == nil {
		t.Fatal("reaction emoji asset was not prepared without a global catalog")
	}
	if image, ok := m.kitty.images[emojiURL]; !ok || !image.loading {
		t.Fatalf("reaction emoji asset was not cached: ok=%v image=%+v", ok, image)
	}
}

func TestAvatarResultsBatchViewportRedraw(t *testing.T) {
	const firstURL = "https://example.social/one.png"
	const secondURL = "https://example.social/two.png"
	m := newModel(nil)
	m.screen = mainScreen
	m.width, m.height = 80, 24
	m.kitty = &kittyRenderer{
		enabled: true,
		images: map[string]*kittyImage{
			firstURL:  {id: 1, placementID: 1, columns: 2, rows: 1, loading: true},
			secondURL: {id: 2, placementID: 2, columns: 2, rows: 1, loading: true},
		},
	}
	m.notes = []Note{{
		Text:           ":one: :two:",
		ReactionEmojis: EmojiRefs{"one": firstURL, "two": secondURL},
	}}
	m.resize()
	before := m.viewport.View()
	if !strings.Contains(before, "◌") {
		t.Fatalf("expected loading placeholders: %q", before)
	}

	updated, _ := m.Update(avatarResult{url: firstURL})
	got := updated.(model)
	if !got.assetRedrawPending || got.viewport.View() != before {
		t.Fatalf("first asset caused an immediate redraw: pending=%v", got.assetRedrawPending)
	}

	updated, _ = got.Update(avatarResult{url: secondURL})
	got = updated.(model)
	if !got.assetRedrawPending || got.viewport.View() != before {
		t.Fatal("second asset caused a redraw before the batch flush")
	}

	updated, _ = got.Update(assetRedrawMsg{})
	got = updated.(model)
	if got.assetRedrawPending || strings.Contains(got.viewport.View(), "◌") {
		t.Fatalf("batched redraw did not update both assets: pending=%v view=%q", got.assetRedrawPending, got.viewport.View())
	}
}

func TestContentWarningHidesAndRevealsNote(t *testing.T) {
	m := newModel(nil)
	m.kitty = &kittyRenderer{enabled: false}
	m.notes = []Note{{
		ID:             "1",
		Text:           "secret",
		ContentWarning: "spoiler",
		Attachments:    []Attachment{{URL: "https://example/image.png", Type: "image/png"}},
	}}

	hidden := m.renderNote(0, 80)
	if !strings.Contains(hidden, "CW: spoiler") || !strings.Contains(hidden, "[非表示のコンテンツ]") || strings.Contains(hidden, "secret") || strings.Contains(hidden, "image.png") {
		t.Fatalf("CW content was not hidden: %q", hidden)
	}

	m.revealedCW["1"] = true
	visible := m.renderNote(0, 80)
	if !strings.Contains(visible, "secret") || !strings.Contains(visible, "image.png") {
		t.Fatalf("CW content was not revealed: %q", visible)
	}
}

func TestCWKeyTogglesSelectedNote(t *testing.T) {
	m := newModel(nil)
	m.screen = mainScreen
	m.focus = contentFocus
	m.notes = []Note{{ID: "1", ContentWarning: "spoiler"}}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got := updated.(model)
	if cmd != nil || !got.revealedCW["1"] {
		t.Fatalf("CW was not revealed: revealed=%v cmd=%v", got.revealedCW["1"], cmd != nil)
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(model)
	if cmd != nil || got.revealedCW["1"] {
		t.Fatalf("CW was not hidden again: revealed=%v cmd=%v", got.revealedCW["1"], cmd != nil)
	}
}

func TestRenderNoteShowsImageAttachmentFallback(t *testing.T) {
	m := newModel(nil)
	m.kitty = &kittyRenderer{enabled: false}
	m.notes = []Note{{
		ID: "1",
		Attachments: []Attachment{
			{URL: "https://example/image.png", Type: "image/png"},
			{URL: "https://example/private.png", Type: "image/png", Sensitive: true},
			{URL: "https://example/video.mp4", Type: "video/mp4"},
		},
	}}

	rendered := m.renderNote(0, 80)
	for _, want := range []string{"[画像] https://example/image.png", "[センシティブ画像]"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("attachment fallback is missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "video.mp4") {
		t.Fatalf("non-image attachment was rendered: %q", rendered)
	}
}

func TestKittySkipsHiddenCWImages(t *testing.T) {
	const imageURL = "https://example/image.png"
	k := &kittyRenderer{enabled: true, images: make(map[string]*kittyImage)}
	note := Note{ID: "1", ContentWarning: "spoiler", Attachments: []Attachment{{URL: imageURL, Type: "image/png"}}}
	if cmds := k.prepare([]Note{note}, map[string]bool{}); len(cmds) != 0 {
		t.Fatalf("hidden CW image was prepared: %d commands", len(cmds))
	}
	if cmds := k.prepare([]Note{note}, map[string]bool{"1": true}); len(cmds) != 1 {
		t.Fatalf("revealed CW image was not prepared: %d commands", len(cmds))
	}
}

func TestKittyPrepareIncludesImageAttachments(t *testing.T) {
	const imageURL = "https://example/image.png"
	k := &kittyRenderer{enabled: true, images: make(map[string]*kittyImage)}
	cmds := k.prepare([]Note{{Attachments: []Attachment{{URL: imageURL, Type: "image/png"}}}})
	if len(cmds) != 1 {
		t.Fatalf("image attachment was not prepared: commands=%d images=%+v", len(cmds), k.images)
	}
	image, ok := k.images[imageURL]
	if !ok || !image.imageAsset || image.columns != imageColumns || image.rows != imageRows {
		t.Fatalf("image asset was not initialized: ok=%v image=%+v", ok, image)
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
	if strings.Contains(got.viewport.View(), string(rune(0x10EEEE))) {
		t.Fatal("viewport refreshed before the redraw batch was flushed")
	}
	updated, _ = got.Update(assetRedrawMsg{})
	got = updated.(model)
	if !strings.Contains(got.viewport.View(), string(rune(0x10EEEE))) {
		t.Fatal("viewport was not refreshed after the redraw batch")
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
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("avatar upload was not batched with redraw: %T", msg)
	}
	for _, child := range batch {
		child()
	}
	if !strings.Contains(output.String(), "a=T,U=1,f=100,i=42") {
		t.Fatalf("avatar was not written directly: %q", output.String())
	}
}

func TestRememberCurrentAccount(t *testing.T) {
	cfg := Config{
		Provider: ProviderMisskey,
		Host:     "https://misskey.example",
		Token:    "token",
		User:     User{ID: "user"},
	}
	cfg.rememberCurrentAccount()
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Host != cfg.Host {
		t.Fatalf("current account was not remembered: %+v", cfg.Accounts)
	}

	cfg.Token = "new-token"
	cfg.rememberCurrentAccount()
	if len(cfg.Accounts) != 1 || cfg.Accounts[0].Token != "new-token" {
		t.Fatalf("existing account was duplicated instead of updated: %+v", cfg.Accounts)
	}
}

func TestSettingsCanStartAddingAccount(t *testing.T) {
	m := newModel(&Config{Host: "https://misskey.example", Token: "token"})
	m.screen = mainScreen
	m.focus = contentFocus

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	updated, _ = updated.(model).Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, _ = updated.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if got.screen != setupScreen || !got.addingAccount || len(got.config.Accounts) != 1 {
		t.Fatalf("account add flow did not start: screen=%v adding=%v accounts=%d", got.screen, got.addingAccount, len(got.config.Accounts))
	}
}

func TestSettingsCanSwitchAccount(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	accounts := []Account{
		{Provider: ProviderMisskey, Host: "https://one.example", Token: "one", User: User{ID: "one"}},
		{Provider: ProviderMastodon, Host: "https://two.example", Token: "two", User: User{ID: "two"}, StatusMaxCharacters: 140},
	}
	m := newModel(&Config{
		Provider: ProviderMisskey,
		Host:     accounts[0].Host,
		Token:    accounts[0].Token,
		User:     accounts[0].User,
		Accounts: accounts,
	})
	m.screen = mainScreen
	m.focus = contentFocus

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	updated, _ = updated.(model).Update(tea.KeyMsg{Type: tea.KeyDown})
	updated, cmd := updated.(model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(model)
	if cmd == nil || got.screen != mainScreen || got.config.Host != accounts[1].Host || got.config.Provider != ProviderMastodon || !got.busy {
		t.Fatalf("account was not switched: screen=%v host=%q provider=%q busy=%v cmd=%v", got.screen, got.config.Host, got.config.Provider, got.busy, cmd != nil)
	}
	if got.composer.CharLimit != accounts[1].StatusMaxCharacters {
		t.Fatalf("account-specific character limit was not applied: %d", got.composer.CharLimit)
	}
	saved, err := loadConfig()
	if err != nil || saved.Host != accounts[1].Host {
		t.Fatalf("switched account was not persisted: cfg=%+v err=%v", saved, err)
	}
}

func TestStaleTimelineResultIsIgnored(t *testing.T) {
	m := newModel(&Config{Host: "https://current.example", Token: "current"})
	m.screen = mainScreen
	m.busy = true

	updated, _ := m.Update(timelineResult{
		accountKey: (Config{Host: "https://old.example", Token: "old"}).accountKey(),
		notes:      []Note{{ID: "old"}},
	})
	got := updated.(model)
	if !got.busy || len(got.notes) != 0 {
		t.Fatalf("stale timeline result was applied: busy=%v notes=%+v", got.busy, got.notes)
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

func TestImageColumnsFitPlaceholderDiacritics(t *testing.T) {
	if imageColumns > len(kittyDiacritics) {
		t.Fatalf("image columns exceed Kitty placeholder diacritics: %d > %d", imageColumns, len(kittyDiacritics))
	}
}

func TestDownloadedImageUsesVirtualPlacement(t *testing.T) {
	const imageURL = "https://example/image"
	var output bytes.Buffer
	k := &kittyRenderer{
		enabled: true,
		output:  &output,
		images: map[string]*kittyImage{
			imageURL: {id: 7, placementID: 8, columns: imageColumns, rows: imageRows, imageAsset: true, loading: true},
		},
	}
	cmd := k.finish(avatarResult{url: imageURL, data: []byte("png"), width: 1600, height: 900})
	if cmd == nil {
		t.Fatal("image upload command is missing")
	}
	cmd()
	if !strings.Contains(output.String(), "a=t,t=d") || !strings.Contains(output.String(), "a=p,U=1") {
		t.Fatalf("image was not uploaded as a virtual placement: %q", output.String())
	}
	if image := k.images[imageURL]; !image.ready || image.columns != imageColumns || image.rows != 3 {
		t.Fatalf("image aspect ratio was not applied: %+v", image)
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
