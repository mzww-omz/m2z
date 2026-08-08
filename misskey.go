package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

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
	ID        string    `json:"id"`
	CreatedAt string    `json:"createdAt"`
	Text      string    `json:"text"`
	Emojis    EmojiRefs `json:"emojis"`
	User      User      `json:"user"`
	Renote    *Note     `json:"renote"`
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

func emojiCatalogCmd(host string) tea.Cmd {
	return func() tea.Msg {
		var raw json.RawMessage
		if err := apiCall(context.Background(), host+"/api/emojis", "", map[string]any{}, &raw); err != nil {
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

func timelineCmd(host, token string, kind int) tea.Cmd {
	return func() tea.Msg {
		var notes []Note
		err := apiCall(context.Background(), host+timelinePath(kind), token, map[string]any{"i": token, "limit": requestLimit}, &notes)
		return timelineResult{notes: notes, err: err}
	}
}

func olderTimelineCmd(host, token string, kind int, untilID string) tea.Cmd {
	return func() tea.Msg {
		var notes []Note
		payload := map[string]any{"i": token, "limit": requestLimit, "untilId": untilID}
		err := apiCall(context.Background(), host+timelinePath(kind), token, payload, &notes)
		return olderTimelineResult{notes: notes, err: err}
	}
}

func timelinePath(kind int) string {
	return []string{"/api/notes/timeline", "/api/notes/local-timeline", "/api/notes/global-timeline"}[min(kind, 2)]
}

func postCmd(host, token, text, replyID string) tea.Cmd {
	return func() tea.Msg {
		payload := map[string]any{"i": token, "text": text}
		if replyID != "" {
			payload["replyId"] = replyID
		}
		err := apiCall(context.Background(), host+"/api/notes/create", token, payload, &struct{}{})
		return postResult{err: err}
	}
}

func apiCall(ctx context.Context, endpoint, token string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("レスポンスの解析に失敗: %w", err)
	}
	return nil
}
