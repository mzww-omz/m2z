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
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Note struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Text      string `json:"text"`
	User      User   `json:"user"`
	Renote    *Note  `json:"renote"`
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

func postCmd(host, token, text string) tea.Cmd {
	return func() tea.Msg {
		err := apiCall(context.Background(), host+"/api/notes/create", token, map[string]any{"i": token, "text": text}, &struct{}{})
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
