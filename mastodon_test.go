package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMastodonAuthURLUsesPKCE(t *testing.T) {
	verifier := "test-verifier"
	parsed, err := url.Parse(mastodonAuthURL("https://example.social", "client", verifier))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected auth query: %v", query)
	}
	if query.Get("code_challenge") != pkceChallenge(verifier) {
		t.Fatal("auth URL has the wrong PKCE challenge")
	}
}

func TestMastodonStreamingURL(t *testing.T) {
	got, err := mastodonStreamingURL("wss://stream.example/", "https://example.social", 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://stream.example/api/v1/streaming/?stream=public%3Alocal" {
		t.Fatalf("streaming URL = %q", got)
	}
	if mastodonStreamName(0) != "user" || mastodonStreamName(2) != "public" {
		t.Fatal("unexpected stream mapping")
	}
}

func TestMastodonHTMLToText(t *testing.T) {
	got := mastodonHTMLToText(`<p>Hello <img alt=":party:" src="emoji.png">!</p><p>Second<br>line</p>`)
	if got != "Hello :party:!\nSecond\nline" {
		t.Fatalf("HTML text = %q", got)
	}
}

func TestMastodonStatusToNote(t *testing.T) {
	status := mastodonStatus{
		ID:          "2",
		CreatedAt:   "2024-01-02T03:04:05.000Z",
		Content:     "<p>boosted</p>",
		SpoilerText: "spoiler",
		Account: mastodonAccount{
			ID:           "account",
			Acct:         "user@example.social",
			DisplayName:  "User",
			AvatarStatic: "https://example.social/avatar.png",
		},
		MediaAttachments: []mastodonMediaAttachment{
			{Type: "image", URL: "https://example.social/image.png", PreviewURL: "https://example.social/preview.png", Description: "画像", Sensitive: true},
			{Type: "video", URL: "https://example.social/video.mp4"},
		},
		Reblog: &mastodonStatus{
			ID:      "1",
			Content: "<p>original</p>",
			Account: mastodonAccount{Acct: "author@example.social"},
		},
	}
	note := mastodonStatusToNote(status)
	if note.ID != "2" || note.User.Username != "user@example.social" || note.ReshareLabel != "ブースト" || note.ContentWarning != "spoiler" || note.Text != "boosted" {
		t.Fatalf("status was not mapped: %+v", note)
	}
	if note.Renote == nil || note.Renote.Text != "original" {
		t.Fatalf("reblog was not mapped: %+v", note.Renote)
	}
	if len(note.Attachments) != 1 || note.Attachments[0].imageURL() != "https://example.social/preview.png" || note.Attachments[0].Description != "画像" || !note.Attachments[0].Sensitive {
		t.Fatalf("media attachments were not mapped: %+v", note.Attachments)
	}
}

func TestMastodonOAuthCommands(t *testing.T) {
	var tokenRequest url.Values
	var accountAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Errorf("app request = %s %q", r.Method, r.Header.Get("Content-Type"))
			}
			_ = r.ParseForm()
			if r.Form.Get("redirect_uris") != mastodonRedirectURI || r.Form.Get("scopes") != mastodonScopes {
				t.Errorf("app form = %v", r.Form)
			}
			_ = json.NewEncoder(w).Encode(mastodonApp{ClientID: "client", ClientSecret: "secret"})
		case "/oauth/token":
			_ = r.ParseForm()
			tokenRequest = r.Form
			_ = json.NewEncoder(w).Encode(mastodonToken{AccessToken: "token"})
		case "/api/v1/accounts/verify_credentials":
			accountAuth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(mastodonAccount{Acct: "user@example.social"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := registerMastodonAppCmd(server.URL)().(mastodonAppResult)
	if app.err != nil || app.clientID != "client" || app.clientSecret != "secret" {
		t.Fatalf("app result = %+v", app)
	}
	result := mastodonTokenCmd(server.URL, app.clientID, app.clientSecret, "code", "verifier")().(authResult)
	if result.err != nil || result.token != "token" || result.user.Username != "user@example.social" {
		t.Fatalf("auth result = %+v", result)
	}
	if tokenRequest.Get("code_verifier") != "verifier" || accountAuth != "Bearer token" {
		t.Fatalf("token request/auth = %v / %q", tokenRequest, accountAuth)
	}
}

func TestMastodonTimelineUsesMaxID(t *testing.T) {
	var gotQuery url.Values
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode([]mastodonStatus{{ID: "1", Account: mastodonAccount{Acct: "user"}}})
	}))
	defer server.Close()

	msg := mastodonTimelineCmd(Config{Host: server.URL, Token: "token"}, 2, "last")()
	result, ok := msg.(olderTimelineResult)
	if !ok || result.err != nil || len(result.notes) != 1 || result.notes[0].ID != "1" {
		t.Fatalf("timeline result = %#v", msg)
	}
	if gotQuery.Get("max_id") != "last" || gotQuery.Get("limit") != "30" || gotAuth != "Bearer token" {
		t.Fatalf("request query/auth = %v / %q", gotQuery, gotAuth)
	}
}

func TestMastodonEmojiCatalogUsesStaticURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"shortcode":"party","url":"animated.png","static_url":"static.png"}]`))
	}))
	defer server.Close()

	result := mastodonEmojiCatalogCmd(Config{Host: server.URL})().(emojiCatalogResult)
	if result.err != nil || len(result.emojis) != 1 || result.emojis[0].Name != "party" || result.emojis[0].URL != "static.png" {
		t.Fatalf("emoji result = %#v", result)
	}
}

func TestConfigWithoutProviderRemainsMisskey(t *testing.T) {
	if (Config{}).provider() != ProviderMisskey {
		t.Fatal("old configuration was not treated as Misskey")
	}
}
