package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/gorilla/websocket"
)

type streamClient struct {
	host  string
	token string
	kind  int

	events chan tea.Msg
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	conn   *websocket.Conn
}

type streamNote struct {
	client *streamClient
	note   Note
}

type streamStatus struct {
	client    *streamClient
	connected bool
	err       error
}

type streamStopped struct{ client *streamClient }

type streamEnvelope struct {
	Type string          `json:"type"`
	Body json.RawMessage `json:"body"`
}

type channelEnvelope struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Body json.RawMessage `json:"body"`
}

func (m *model) ensureStream() tea.Cmd {
	if m.stream != nil && m.stream.kind == m.menu {
		return nil
	}
	if m.stream != nil {
		m.stream.close()
	}
	m.stream = newStreamClient(m.host, m.config.Token, m.menu)
	return m.stream.next()
}

func (m *model) stopStream() {
	if m.stream != nil {
		m.stream.close()
		m.stream = nil
	}
}

func newStreamClient(host, token string, kind int) *streamClient {
	s := &streamClient{
		host:   host,
		token:  token,
		kind:   kind,
		events: make(chan tea.Msg, 16),
		done:   make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *streamClient) next() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-s.events:
			return msg
		case <-s.done:
			return streamStopped{client: s}
		}
	}
}

func (s *streamClient) close() {
	s.once.Do(func() {
		close(s.done)
		s.mu.Lock()
		if s.conn != nil {
			_ = s.conn.Close()
		}
		s.mu.Unlock()
	})
}

func (s *streamClient) run() {
	for {
		if s.isClosed() {
			return
		}
		conn, err := s.connect()
		if err != nil {
			s.emit(streamStatus{client: s, err: err})
			if !s.wait(3 * time.Second) {
				return
			}
			continue
		}
		s.setConn(conn)
		s.emit(streamStatus{client: s, connected: true})
		s.read(conn)
		s.clearConn(conn)
		_ = conn.Close()
		if s.isClosed() {
			return
		}
		s.emit(streamStatus{client: s, err: errors.New("接続が切断されました")})
		if !s.wait(3 * time.Second) {
			return
		}
	}
}

func (s *streamClient) connect() (*websocket.Conn, error) {
	endpoint, err := streamingURL(s.host, s.token)
	if err != nil {
		return nil, err
	}
	conn, _, err := (&websocket.Dialer{HandshakeTimeout: 20 * time.Second}).Dial(endpoint, nil)
	if err != nil {
		return nil, err
	}
	message := map[string]any{
		"type": "connect",
		"body": map[string]any{
			"channel": channelName(s.kind),
			"id":      newSession(),
		},
	}
	if err := conn.WriteJSON(message); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (s *streamClient) read(conn *websocket.Conn) {
	for {
		var message streamEnvelope
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		if message.Type != "channel" {
			continue
		}
		var channel channelEnvelope
		if json.Unmarshal(message.Body, &channel) != nil || channel.Type != "note" {
			continue
		}
		var note Note
		if json.Unmarshal(channel.Body, &note) != nil || note.ID == "" {
			continue
		}
		s.emit(streamNote{client: s, note: note})
	}
}

func (s *streamClient) emit(msg tea.Msg) {
	select {
	case s.events <- msg:
	case <-s.done:
	}
}

func (s *streamClient) wait(duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.done:
		return false
	}
}

func (s *streamClient) isClosed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *streamClient) setConn(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		_ = conn.Close()
	default:
		s.conn = conn
	}
}

func (s *streamClient) clearConn(conn *websocket.Conn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
}

func streamingURL(host, token string) (string, error) {
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		return "", errors.New("ストリーミングURLを作成できません")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else if u.Scheme == "http" {
		u.Scheme = "ws"
	} else {
		return "", errors.New("httpまたはhttpsのホストが必要です")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/streaming"
	query := u.Query()
	query.Set("i", token)
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func channelName(kind int) string {
	switch kind {
	case 1:
		return "localTimeline"
	case 2:
		return "globalTimeline"
	default:
		return "homeTimeline"
	}
}

func (m model) streamNote(msg streamNote) (model, tea.Cmd) {
	if m.stream != msg.client {
		return m, nil
	}
	for _, note := range m.notes {
		if note.ID == msg.note.ID {
			return m, m.stream.next()
		}
	}
	if m.selected > 0 {
		m.selected++
	}
	m.notes = append([]Note{msg.note}, m.notes...)
	m.status, m.err = "リアルタイム更新", nil
	m.updateViewport()
	return m, m.stream.next()
}

func (m model) streamMessage(msg tea.Msg) (model, tea.Cmd) {
	switch event := msg.(type) {
	case streamNote:
		return m.streamNote(event)
	case streamStatus:
		if m.stream != event.client {
			return m, nil
		}
		if event.connected {
			m.status, m.err = "リアルタイム接続中", nil
		} else if event.err != nil {
			m.status, m.err = "リアルタイム接続を再試行中", event.err
		}
		return m, m.stream.next()
	case streamStopped:
		return m, nil
	default:
		return m, nil
	}
}
