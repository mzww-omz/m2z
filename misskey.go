package main

import (
	"bytes"
	"context"
	"encoding/json"

	tea "github.com/charmbracelet/bubbletea"
)

type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl"`
}

type EmojiRefs map[string]string

func (e *EmojiRefs) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" {
		*e = nil
		return nil
	}
	var object map[string]string
	if len(data) > 0 && data[0] == '{' {
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
		*e = object
		return nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return err
	}
	object = make(map[string]string, len(names))
	for _, name := range names {
		object[name] = ""
	}
	*e = object
	return nil
}

type Note struct {
	ID           string         `json:"id"`
	CreatedAt    string         `json:"createdAt"`
	Text         string         `json:"text"`
	Emojis       EmojiRefs      `json:"emojis"`
	User         User           `json:"user"`
	Renote       *Note          `json:"renote"`
	ReshareLabel string         `json:"-"`
	Reactions    map[string]int `json:"reactions"`
	MyReaction   *string        `json:"myReaction"`
}

type Meta struct {
	Name         string `json:"name"`
	URI          string `json:"uri"`
	SoftwareName string `json:"softwareName"`
}

type timelineResult struct {
	notes []Note
	err   error
}

type olderTimelineResult struct {
	notes []Note
	err   error
}

type postResult struct{ err error }
type reactionResult struct{ err error }
type renoteResult struct{ err error }

func emojiCatalogCmd(cfg Config) tea.Cmd {
	if cfg.provider() == ProviderMastodon {
		return mastodonEmojiCatalogCmd(cfg)
	}
	return func() tea.Msg {
		var raw json.RawMessage
		if err := apiCall(context.Background(), cfg.Host+"/api/emojis", "", map[string]any{}, &raw); err != nil {
			return emojiCatalogResult{err: err}
		}
		var emojis []CustomEmoji
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			if err := json.Unmarshal(trimmed, &emojis); err != nil {
				return emojiCatalogResult{err: err}
			}
		} else {
			var response struct {
				Emojis []CustomEmoji `json:"emojis"`
			}
			if err := json.Unmarshal(trimmed, &response); err != nil {
				return emojiCatalogResult{err: err}
			}
			emojis = response.Emojis
		}
		return emojiCatalogResult{emojis: emojis}
	}
}

func timelineCmd(cfg Config, kind int) tea.Cmd {
	if cfg.provider() == ProviderMastodon {
		return mastodonTimelineCmd(cfg, kind, "")
	}
	return func() tea.Msg {
		var notes []Note
		err := apiCall(context.Background(), cfg.Host+timelinePath(kind), cfg.Token, map[string]any{"i": cfg.Token, "limit": requestLimit}, &notes)
		return timelineResult{notes: notes, err: err}
	}
}

func olderTimelineCmd(cfg Config, kind int, untilID string) tea.Cmd {
	if cfg.provider() == ProviderMastodon {
		return mastodonTimelineCmd(cfg, kind, untilID)
	}
	return func() tea.Msg {
		var notes []Note
		payload := map[string]any{"i": cfg.Token, "limit": requestLimit, "untilId": untilID}
		err := apiCall(context.Background(), cfg.Host+timelinePath(kind), cfg.Token, payload, &notes)
		return olderTimelineResult{notes: notes, err: err}
	}
}

func timelinePath(kind int) string {
	return []string{"/api/notes/timeline", "/api/notes/local-timeline", "/api/notes/global-timeline"}[min(kind, 2)]
}

func postCmd(cfg Config, text, replyID string) tea.Cmd {
	if cfg.provider() == ProviderMastodon {
		return mastodonPostCmd(cfg, text, replyID)
	}
	return func() tea.Msg {
		payload := map[string]any{"i": cfg.Token, "text": text}
		if replyID != "" {
			payload["replyId"] = replyID
		}
		err := apiCall(context.Background(), cfg.Host+"/api/notes/create", cfg.Token, payload, &struct{}{})
		return postResult{err: err}
	}
}

func reactionCmd(host, token string, note Note, reaction string) tea.Cmd {
	target := actionNote(note)
	if target.MyReaction != nil && *target.MyReaction == reaction {
		return reactionDeleteCmd(host, token, target.ID)
	}
	return reactionCreateCmd(host, token, target.ID, reaction)
}

func reactionCreateCmd(host, token, noteID, reaction string) tea.Cmd {
	return func() tea.Msg {
		err := apiCall(context.Background(), host+"/api/notes/reactions/create", token, map[string]any{
			"i":        token,
			"noteId":   noteID,
			"reaction": reaction,
		}, &struct{}{})
		return reactionResult{err: err}
	}
}

func reactionDeleteCmd(host, token, noteID string) tea.Cmd {
	return func() tea.Msg {
		err := apiCall(context.Background(), host+"/api/notes/reactions/delete", token, map[string]any{
			"i":      token,
			"noteId": noteID,
		}, &struct{}{})
		return reactionResult{err: err}
	}
}

func renoteCmd(host, token string, note Note) tea.Cmd {
	target := actionNote(note)
	return func() tea.Msg {
		var result struct {
			CreatedNote Note `json:"createdNote"`
		}
		err := apiCall(context.Background(), host+"/api/notes/create", token, map[string]any{
			"i":        token,
			"renoteId": target.ID,
		}, &result)
		return renoteResult{err: err}
	}
}

func actionNote(note Note) Note {
	if note.Renote != nil {
		return *note.Renote
	}
	return note
}
