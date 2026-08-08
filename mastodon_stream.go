package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

func newMastodonStreamClient(cfg Config, kind int) *streamClient {
	s := &streamClient{
		host:   cfg.Host,
		token:  cfg.Token,
		kind:   kind,
		events: make(chan tea.Msg, 16),
		done:   make(chan struct{}),
	}
	s.connectFn = func() (*websocket.Conn, error) {
		endpoint, err := mastodonStreamingURL(cfg.StreamingURL, cfg.Host, kind)
		if err != nil {
			return nil, err
		}
		header := http.Header{}
		header.Set("Authorization", "Bearer "+cfg.Token)
		conn, _, err := (&websocket.Dialer{HandshakeTimeout: 20 * time.Second}).Dial(endpoint, header)
		return conn, err
	}
	s.readFn = func(conn *websocket.Conn) {
		for {
			var message mastodonStreamEnvelope
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			if message.Event != "update" {
				continue
			}
			payload := message.Payload
			var encoded string
			if json.Unmarshal(payload, &encoded) == nil {
				payload = []byte(encoded)
			}
			var status mastodonStatus
			if json.Unmarshal(payload, &status) != nil || status.ID == "" {
				continue
			}
			s.emit(streamNote{client: s, note: mastodonStatusToNote(status)})
		}
	}
	go s.run()
	return s
}

type mastodonStreamEnvelope struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

func mastodonStreamingURL(streamingHost, host string, kind int) (string, error) {
	if streamingHost == "" {
		streamingHost = host
	}
	u, err := url.Parse(streamingHost)
	if err != nil || u.Host == "" {
		return "", errors.New("MastodonストリーミングURLを作成できません")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", errors.New("http、https、wsまたはwssのストリーミングURLが必要です")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/api/v1/streaming") {
		path += "/api/v1/streaming"
	}
	u.Path = path + "/"
	query := u.Query()
	query.Set("stream", mastodonStreamName(kind))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func mastodonStreamName(kind int) string {
	switch kind {
	case 1:
		return "public:local"
	case 2:
		return "public"
	default:
		return "user"
	}
}
