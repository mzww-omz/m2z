package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func apiCall(ctx context.Context, endpoint, token string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return apiRequest(ctx, http.MethodPost, endpoint, token, bytes.NewReader(data), "application/json", out)
}

func apiGet(ctx context.Context, endpoint, token string, out any) error {
	return apiRequest(ctx, http.MethodGet, endpoint, token, nil, "", out)
}

func apiForm(ctx context.Context, method, endpoint, token string, values url.Values, out any) error {
	return apiRequest(ctx, method, endpoint, token, strings.NewReader(values.Encode()), "application/x-www-form-urlencoded", out)
}

func apiRequest(ctx context.Context, method, endpoint, token string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("レスポンスの解析に失敗: %w", err)
	}
	return nil
}
