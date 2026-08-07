package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxAvatarBytes     = 8 << 20
	maxAvatarDimension = 2048
	avatarSize         = 128
	kittyColumns       = 4
	kittyRows          = 2
	kittyChunkSize     = 4096
)

var kittyDiacritics = []rune{
	'\u0305', '\u030d', '\u030e', '\u0310',
	'\u0312', '\u033d', '\u033e', '\u033f',
}

type avatarResult struct {
	url  string
	data []byte
	err  error
}

type kittyImage struct {
	id       uint32
	loading  bool
	ready    bool
	uploaded bool
	data     []byte
}

type kittyRenderer struct {
	enabled      bool
	nextID       uint32
	images       map[string]*kittyImage
	clearPending bool
}

func newKittyRenderer() *kittyRenderer {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("M2Z_GRAPHICS")))
	enabled := mode == "kitty" || (mode != "off" && kittyTerminal())
	return &kittyRenderer{
		enabled: enabled,
		nextID:  1,
		images:  make(map[string]*kittyImage),
	}
}

func kittyTerminal() bool {
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return true
	}
	term := strings.ToLower(os.Getenv("TERM"))
	program := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	return strings.Contains(term, "kitty") || program == "ghostty" || program == "wezterm"
}

func (k *kittyRenderer) prepare(notes []Note) []tea.Cmd {
	if k == nil || !k.enabled {
		return nil
	}
	var cmds []tea.Cmd
	for _, note := range notes {
		avatarURL := strings.TrimSpace(note.User.AvatarURL)
		if avatarURL == "" {
			continue
		}
		if _, ok := k.images[avatarURL]; ok {
			continue
		}
		k.images[avatarURL] = &kittyImage{id: k.nextID, loading: true}
		k.nextID++
		cmds = append(cmds, avatarCmd(avatarURL))
	}
	return cmds
}

func (m *model) loadAvatars(notes []Note) tea.Cmd {
	if m.kitty == nil {
		return nil
	}
	return batchCommands(m.kitty.prepare(notes)...)
}

func (m *model) resetAvatarCache() tea.Cmd {
	if m.kitty == nil {
		return nil
	}
	m.kitty.reset()
	return m.loadAvatars(m.notes)
}

func (k *kittyRenderer) reset() {
	if k == nil || !k.enabled {
		return
	}
	k.images = make(map[string]*kittyImage)
	k.nextID = 1
	k.clearPending = true
}

func (k *kittyRenderer) finish(msg avatarResult) {
	if k == nil {
		return
	}
	img, ok := k.images[msg.url]
	if !ok {
		return
	}
	img.loading = false
	if msg.err != nil {
		delete(k.images, msg.url)
		return
	}
	img.data = msg.data
	img.ready = true
}

func (m model) avatarPlaceholder(avatarURL string) string {
	if m.kitty == nil {
		return ""
	}
	return m.kitty.placeholder(avatarURL)
}

func (k *kittyRenderer) placeholder(avatarURL string) string {
	if k == nil || !k.enabled {
		return ""
	}
	img, ok := k.images[strings.TrimSpace(avatarURL)]
	if !ok {
		return ""
	}
	if !img.ready {
		return kittyLoadingPlaceholder()
	}
	return kittyPlaceholder(img.id)
}

func kittyLoadingPlaceholder() string {
	line := " " + "◌" + strings.Repeat(" ", kittyColumns-2)
	return line + "\n" + strings.Repeat(" ", kittyColumns)
}

func (k *kittyRenderer) takeUploads() string {
	if k == nil || !k.enabled {
		return ""
	}
	urls := make([]string, 0, len(k.images))
	for avatarURL, img := range k.images {
		if img.ready && !img.uploaded {
			urls = append(urls, avatarURL)
		}
	}
	sort.Strings(urls)
	var out strings.Builder
	if k.clearPending {
		out.WriteString("\x1b_Ga=d,d=A,q=2;\x1b\\")
		k.clearPending = false
	}
	for _, avatarURL := range urls {
		img := k.images[avatarURL]
		out.WriteString(kittyUpload(img.data, img.id))
		img.uploaded = true
	}
	return out.String()
}

func avatarCmd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		data, err := downloadAvatar(rawURL)
		return avatarResult{url: rawURL, data: data, err: err}
	}
}

func downloadAvatar(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("アイコンURLが不正です")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("アイコン取得 HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAvatarBytes {
		return nil, errors.New("アイコンが大きすぎます")
	}
	return avatarPNG(data)
}

func avatarPNG(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("アイコン形式に未対応: %w", err)
	}
	if config.Width < 1 || config.Height < 1 || config.Width > maxAvatarDimension || config.Height > maxAvatarDimension {
		return nil, errors.New("アイコンのサイズが大きすぎます")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("アイコンのデコードに失敗: %w", err)
	}
	if config.Width > avatarSize || config.Height > avatarSize {
		scale := float64(avatarSize) / float64(max(config.Width, config.Height))
		width := max(1, int(float64(config.Width)*scale))
		height := max(1, int(float64(config.Height)*scale))
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		xdraw.ApproxBiLinear.Scale(resized, resized.Bounds(), img, img.Bounds(), xdraw.Over, nil)
		img = resized
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func kittyUpload(data []byte, id uint32) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var out strings.Builder
	for start := 0; start < len(encoded); start += kittyChunkSize {
		end := min(start+kittyChunkSize, len(encoded))
		more := end < len(encoded)
		mode := 0
		if more {
			mode = 1
		}
		control := fmt.Sprintf("m=%d", mode)
		if start == 0 {
			control = fmt.Sprintf("a=t,f=100,i=%d,q=2,%s", id, control)
		}
		out.WriteString("\x1b_G")
		out.WriteString(control)
		out.WriteByte(';')
		out.WriteString(encoded[start:end])
		out.WriteString("\x1b\\")
	}
	out.WriteString(fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,c=%d,r=%d\x1b\\", id, kittyColumns, kittyRows))
	return out.String()
}

func kittyPlaceholder(id uint32) string {
	low := id & 0xFFFFFF
	red, green, blue := byte(low>>16), byte(low>>8), byte(low)
	var out strings.Builder
	for row := 0; row < kittyRows; row++ {
		if row > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm", red, green, blue)
		for column := 0; column < kittyColumns; column++ {
			out.WriteRune('\U0010EEEE')
			out.WriteRune(kittyDiacritic(uint32(row)))
			out.WriteRune(kittyDiacritic(uint32(column)))
			if high := id >> 24; high > 0 {
				out.WriteRune(kittyDiacritic(high))
			}
		}
		out.WriteString("\x1b[39m")
	}
	return out.String()
}

func kittyDiacritic(value uint32) rune {
	if value >= uint32(len(kittyDiacritics)) {
		return kittyDiacritics[0]
	}
	return kittyDiacritics[value]
}

func batchCommands(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return tea.Batch(filtered...)
}
