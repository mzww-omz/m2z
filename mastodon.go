package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	xhtml "golang.org/x/net/html"
)

const (
	mastodonRedirectURI = "urn:ietf:wg:oauth:2.0:oob"
	mastodonScopes      = "read:accounts read:statuses write:statuses"
)

type mastodonInstanceInfo struct {
	URI      string `json:"uri"`
	Title    string `json:"title"`
	Version  string `json:"version"`
	Software struct {
		Name string `json:"name"`
	} `json:"software"`
	Configuration struct {
		URLs struct {
			Streaming string `json:"streaming"`
		} `json:"urls"`
		Statuses struct {
			MaxCharacters int `json:"max_characters"`
		} `json:"statuses"`
	} `json:"configuration"`
}

type mastodonApp struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type mastodonToken struct {
	AccessToken string `json:"access_token"`
}

type mastodonAccount struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Acct         string `json:"acct"`
	DisplayName  string `json:"display_name"`
	Avatar       string `json:"avatar"`
	AvatarStatic string `json:"avatar_static"`
}

type mastodonMediaAttachment struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	PreviewURL  string `json:"preview_url"`
	Description string `json:"description"`
}

type mastodonStatus struct {
	ID               string                    `json:"id"`
	CreatedAt        string                    `json:"created_at"`
	Content          string                    `json:"content"`
	SpoilerText      string                    `json:"spoiler_text"`
	Account          mastodonAccount           `json:"account"`
	MediaAttachments []mastodonMediaAttachment `json:"media_attachments"`
	Reblog           *mastodonStatus           `json:"reblog"`
}

type mastodonEmoji struct {
	Shortcode string `json:"shortcode"`
	URL       string `json:"url"`
	StaticURL string `json:"static_url"`
}

func probeMastodon(ctx context.Context, host string) (mastodonInstanceInfo, error) {
	var instance mastodonInstanceInfo
	if err := apiGet(ctx, host+"/api/v2/instance", "", &instance); err == nil && instance.isMastodon() {
		return instance, nil
	}

	var legacy mastodonInstanceInfo
	if err := apiGet(ctx, host+"/api/v1/instance", "", &legacy); err != nil {
		return mastodonInstanceInfo{}, err
	}
	if !legacy.isMastodon() {
		return mastodonInstanceInfo{}, errNotMastodon
	}
	return legacy, nil
}

func (i mastodonInstanceInfo) isMastodon() bool {
	return strings.EqualFold(i.Software.Name, "mastodon") || (i.Version != "" && (i.URI != "" || i.Title != ""))
}

func registerMastodonAppCmd(host string) tea.Cmd {
	return func() tea.Msg {
		var app mastodonApp
		values := url.Values{
			"client_name":   {appName},
			"redirect_uris": {mastodonRedirectURI},
			"scopes":        {mastodonScopes},
		}
		if err := apiForm(context.Background(), http.MethodPost, host+"/api/v1/apps", "", values, &app); err != nil {
			return mastodonAppResult{err: err}
		}
		if app.ClientID == "" || app.ClientSecret == "" {
			return mastodonAppResult{err: errInvalidMastodonApp}
		}
		return mastodonAppResult{clientID: app.ClientID, clientSecret: app.ClientSecret}
	}
}

func mastodonAuthURL(host, clientID, verifier string) string {
	values := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {mastodonRedirectURI},
		"scope":                 {mastodonScopes},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}
	return strings.TrimRight(host, "/") + "/oauth/authorize?" + values.Encode()
}

func mastodonTokenCmd(host, clientID, clientSecret, code, verifier string) tea.Cmd {
	return func() tea.Msg {
		var token mastodonToken
		values := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"client_id":     {clientID},
			"client_secret": {clientSecret},
			"redirect_uri":  {mastodonRedirectURI},
			"code_verifier": {verifier},
		}
		if err := apiForm(context.Background(), http.MethodPost, host+"/oauth/token", "", values, &token); err != nil {
			return authResult{err: err}
		}
		if token.AccessToken == "" {
			return authResult{err: errInvalidMastodonToken}
		}

		var account mastodonAccount
		if err := apiGet(context.Background(), host+"/api/v1/accounts/verify_credentials", token.AccessToken, &account); err != nil {
			return authResult{err: err}
		}
		return authResult{token: token.AccessToken, user: mastodonAccountToUser(account)}
	}
}

func newPKCEVerifier() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return strings.ReplaceAll(newSession()+newSession(), "-", "")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func mastodonTimelineCmd(cfg Config, kind int, maxID string) tea.Cmd {
	return func() tea.Msg {
		values := url.Values{"limit": {strconv.Itoa(requestLimit)}}
		if maxID != "" {
			values.Set("max_id", maxID)
		}
		path := "/api/v1/timelines/public"
		if kind == 0 {
			path = "/api/v1/timelines/home"
		} else if kind == 1 {
			values.Set("local", "true")
		}
		var statuses []mastodonStatus
		err := apiGet(context.Background(), cfg.Host+path+"?"+values.Encode(), cfg.Token, &statuses)
		notes := make([]Note, 0, len(statuses))
		for _, status := range statuses {
			notes = append(notes, mastodonStatusToNote(status))
		}
		if maxID == "" {
			return timelineResult{accountKey: cfg.accountKey(), notes: notes, err: err}
		}
		return olderTimelineResult{accountKey: cfg.accountKey(), notes: notes, err: err}
	}
}

func mastodonPostCmd(cfg Config, text, replyID string) tea.Cmd {
	return func() tea.Msg {
		values := url.Values{"status": {text}}
		if replyID != "" {
			values.Set("in_reply_to_id", replyID)
		}
		return postResult{err: apiForm(context.Background(), http.MethodPost, cfg.Host+"/api/v1/statuses", cfg.Token, values, &mastodonStatus{})}
	}
}

func mastodonEmojiCatalogCmd(cfg Config) tea.Cmd {
	return func() tea.Msg {
		var source []mastodonEmoji
		if err := apiGet(context.Background(), cfg.Host+"/api/v1/custom_emojis", cfg.Token, &source); err != nil {
			return emojiCatalogResult{accountKey: cfg.accountKey(), err: err}
		}
		emojis := make([]CustomEmoji, 0, len(source))
		for _, emoji := range source {
			imageURL := emoji.StaticURL
			if imageURL == "" {
				imageURL = emoji.URL
			}
			if emoji.Shortcode != "" && imageURL != "" {
				emojis = append(emojis, CustomEmoji{Name: emoji.Shortcode, URL: imageURL})
			}
		}
		return emojiCatalogResult{accountKey: cfg.accountKey(), emojis: emojis}
	}
}

func mastodonStatusToNote(status mastodonStatus) Note {
	note := Note{
		ID:           status.ID,
		CreatedAt:    status.CreatedAt,
		Text:         mastodonHTMLToText(status.Content),
		User:         mastodonAccountToUser(status.Account),
		Attachments:  mastodonAttachmentsToAttachments(status.MediaAttachments),
		ReshareLabel: "ブースト",
	}
	if status.SpoilerText != "" {
		note.Text = "CW: " + html.UnescapeString(status.SpoilerText) + "\n" + note.Text
	}
	if status.Reblog != nil {
		reblog := mastodonStatusToNote(*status.Reblog)
		note.Renote = &reblog
	}
	return note
}

func mastodonAttachmentsToAttachments(source []mastodonMediaAttachment) []Attachment {
	attachments := make([]Attachment, 0, len(source))
	for _, attachment := range source {
		if attachment.Type != "image" || attachment.URL == "" && attachment.PreviewURL == "" {
			continue
		}
		attachments = append(attachments, Attachment{
			URL:         attachment.URL,
			PreviewURL:  attachment.PreviewURL,
			Type:        attachment.Type,
			Description: attachment.Description,
		})
	}
	return attachments
}

func mastodonAccountToUser(account mastodonAccount) User {
	username := account.Acct
	if username == "" {
		username = account.Username
	}
	avatar := account.AvatarStatic
	if avatar == "" {
		avatar = account.Avatar
	}
	return User{ID: account.ID, Username: username, Name: html.UnescapeString(account.DisplayName), AvatarURL: avatar}
}

func mastodonHTMLToText(source string) string {
	tokenizer := xhtml.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	for {
		tokenType := tokenizer.Next()
		switch tokenType {
		case xhtml.ErrorToken:
			return cleanMastodonText(out.String())
		case xhtml.TextToken:
			out.WriteString(tokenizer.Token().Data)
		case xhtml.StartTagToken:
			token := tokenizer.Token()
			switch strings.ToLower(token.Data) {
			case "br":
				appendMastodonNewline(&out)
			case "p", "div", "li", "blockquote":
				appendMastodonNewline(&out)
			case "img":
				if emoji := mastodonImageEmoji(token); emoji != "" {
					out.WriteString(emoji)
				}
			}
		case xhtml.EndTagToken:
			switch strings.ToLower(tokenizer.Token().Data) {
			case "p", "div", "li", "blockquote":
				appendMastodonNewline(&out)
			}
		}
	}
}

func mastodonImageEmoji(token xhtml.Token) string {
	for _, attr := range token.Attr {
		if (attr.Key == "alt" || attr.Key == "title") && strings.HasPrefix(attr.Val, ":") && strings.HasSuffix(attr.Val, ":") {
			return attr.Val
		}
	}
	return ""
}

func appendMastodonNewline(out *strings.Builder) {
	if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
		out.WriteByte('\n')
	}
}

func cleanMastodonText(source string) string {
	lines := strings.Split(strings.TrimSpace(source), "\n")
	cleaned := lines[:0]
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

var (
	errNotMastodon          = errors.New("Mastodonではありません")
	errInvalidMastodonApp   = errors.New("Mastodonアプリ情報を取得できませんでした")
	errInvalidMastodonToken = errors.New("アクセストークンを取得できませんでした")
)
