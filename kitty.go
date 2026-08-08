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
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	maxAvatarBytes     = 8 << 20
	maxAvatarDimension = 2048
	avatarSize         = 128
	imageColumns       = 8
	imageRows          = 8
	kittyColumns       = 4
	kittyRows          = 2
	kittyChunkSize     = 4096
)

var kittyDiacritics = []rune{
	'\u0305', '\u030d', '\u030e', '\u0310',
	'\u0312', '\u033d', '\u033e', '\u033f',
}

type avatarResult struct {
	url    string
	data   []byte
	width  int
	height int
	err    error
}

type kittyImage struct {
	id          uint32
	placementID uint32
	columns     int
	rows        int
	loading     bool
	ready       bool
	autoSize    bool
	imageAsset  bool
}

type kittyRenderer struct {
	enabled bool
	nextID  uint32
	images  map[string]*kittyImage
	output  io.Writer
}

type synchronizedOutput struct {
	*os.File
	mu sync.Mutex
}

func (w *synchronizedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.File.Write(data)
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
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || program == "ghostty" || program == "wezterm"
}

func (k *kittyRenderer) prepare(notes []Note, revealed ...map[string]bool) []tea.Cmd {
	if k == nil || !k.enabled {
		return nil
	}
	var revealedCW map[string]bool
	if len(revealed) > 0 {
		revealedCW = revealed[0]
	}
	var cmds []tea.Cmd
	for _, rawNote := range notes {
		if cmd := k.prepareAsset(rawNote.User.AvatarURL, kittyColumns, kittyRows); cmd != nil {
			cmds = append(cmds, cmd)
		}
		note := actionNote(rawNote)
		isRevealed := revealedCW[rawNote.ID]
		if contentWarning(rawNote) != "" && !isRevealed {
			continue
		}
		for _, attachment := range note.Attachments {
			if !attachment.isImage() || (attachment.Sensitive && !isRevealed) {
				continue
			}
			if cmd := k.prepareImageAsset(attachment.imageURL()); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

func (k *kittyRenderer) prepareAsset(rawURL string, columns, rows int) tea.Cmd {
	return k.prepareAssetMode(rawURL, columns, rows, false, false)
}

func (k *kittyRenderer) prepareEmojiAsset(rawURL string, columns, rows int) tea.Cmd {
	return k.prepareAssetMode(rawURL, columns, rows, true, false)
}

func (k *kittyRenderer) prepareImageAsset(rawURL string) tea.Cmd {
	return k.prepareAssetMode(rawURL, imageColumns, imageRows, false, true)
}

func (k *kittyRenderer) prepareAssetMode(rawURL string, columns, rows int, autoSize, imageAsset bool) tea.Cmd {
	if k == nil || !k.enabled {
		return nil
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil
	}
	if _, ok := k.images[rawURL]; ok {
		return nil
	}
	k.images[rawURL] = &kittyImage{
		id:          k.nextID,
		placementID: k.nextID,
		columns:     columns,
		rows:        rows,
		loading:     true,
		autoSize:    autoSize,
		imageAsset:  imageAsset,
	}
	k.nextID++
	return avatarCmd(rawURL)
}

func (m *model) loadAvatars(notes []Note) tea.Cmd {
	if m.kitty == nil {
		return nil
	}
	return batchCommands(m.kitty.prepare(notes, m.revealedCW)...)
}

func (m *model) resetImageCache() tea.Cmd {
	if m.kitty == nil {
		return nil
	}
	m.kitty.reset()
	return tea.Sequence(m.kitty.clearCmd(), batchCommands(m.loadAvatars(m.notes), m.loadEmojiAssets(m.notes)))
}

func (k *kittyRenderer) reset() {
	if k == nil || !k.enabled {
		return
	}
	k.images = make(map[string]*kittyImage)
	k.nextID = 1
}

func (k *kittyRenderer) finish(msg avatarResult) tea.Cmd {
	if k == nil {
		return nil
	}
	img, ok := k.images[msg.url]
	if !ok {
		return nil
	}
	img.loading = false
	if msg.err != nil {
		delete(k.images, msg.url)
		return nil
	}
	if img.autoSize && msg.width > 0 && msg.height > 0 {
		img.columns, img.rows = emojiDimensions(CustomEmoji{Width: msg.width, Height: msg.height})
	}
	if img.imageAsset && msg.width > 0 && msg.height > 0 {
		img.columns, img.rows = imageDimensions(msg.width, msg.height)
	}
	img.ready = true
	return k.writeCmd(kittyUploadMode(msg.data, img.id, img.columns, img.rows, img.autoSize || img.imageAsset, img.placementID))
}

func imageDimensions(width, height int) (int, int) {
	if width < 1 || height < 1 {
		return imageColumns, imageRows
	}
	aspect := float64(width) / float64(height)
	rows := max(3, min(imageRows, int(math.Round(float64(imageColumns)/(aspect*2)))))
	columns := int(math.Round(aspect * float64(rows) * 2))
	columns = max(4, min(imageColumns, columns))
	if columns == imageColumns {
		rows = max(3, min(imageRows, int(math.Round(float64(columns)/(aspect*2)))))
	}
	return columns, rows
}

func (m model) avatarPlaceholder(avatarURL string) string {
	if m.kitty == nil {
		return ""
	}
	return m.kitty.placeholder(avatarURL)
}

func (k *kittyRenderer) placeholder(avatarURL string) string {
	return k.placeholderFor(avatarURL, kittyColumns, kittyRows)
}

func (k *kittyRenderer) placeholderFor(rawURL string, columns, rows int) string {
	if k == nil || !k.enabled {
		return ""
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return kittyMissingPlaceholder()
	}
	img, ok := k.images[rawURL]
	if !ok || !img.ready {
		return kittyLoadingPlaceholder(columns, rows)
	}
	return kittyPlaceholderWithPlacement(img.id, img.columns, img.rows, img.placementID)
}

func kittyLoadingPlaceholder(columns, rows int) string {
	line := " " + "◌" + strings.Repeat(" ", max(0, columns-2))
	return line + strings.Repeat("\n"+strings.Repeat(" ", columns), max(0, rows-1))
}

func kittyMissingPlaceholder() string {
	line := "  · "
	return line + "\n" + strings.Repeat(" ", kittyColumns)
}

func (k *kittyRenderer) clearCmd() tea.Cmd {
	return k.writeCmd("\x1b_Ga=d,d=A,q=2;\x1b\\")
}

func (k *kittyRenderer) writeCmd(sequence string) tea.Cmd {
	if k == nil || !k.enabled || k.output == nil {
		return nil
	}
	return func() tea.Msg {
		_, _ = io.WriteString(k.output, sequence)
		return nil
	}
}

func avatarCmd(rawURL string) tea.Cmd {
	return func() tea.Msg {
		data, width, height, err := downloadAvatar(rawURL)
		return avatarResult{url: rawURL, data: data, width: width, height: height, err: err}
	}
}

func downloadAvatar(rawURL string) ([]byte, int, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, 0, 0, errors.New("アイコンURLが不正です")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(u.String())
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, 0, fmt.Errorf("アイコン取得 HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return nil, 0, 0, err
	}
	if len(data) > maxAvatarBytes {
		return nil, 0, 0, errors.New("アイコンが大きすぎます")
	}
	normalized, err := avatarPNG(data)
	if err != nil {
		return nil, 0, 0, err
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(normalized))
	if err != nil {
		return nil, 0, 0, err
	}
	return normalized, config.Width, config.Height, nil
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

func kittyUpload(data []byte, id uint32, columns, rows int) string {
	return kittyUploadMode(data, id, columns, rows, false, 0)
}

func kittyUploadMode(data []byte, id uint32, columns, rows int, virtualPlacement bool, placementID uint32) string {
	encoded := base64.StdEncoding.EncodeToString(data)
	var out strings.Builder
	placement := ""
	if placementID > 0 {
		placement = fmt.Sprintf(",p=%d", placementID)
	}
	for start := 0; start < len(encoded); start += kittyChunkSize {
		end := min(start+kittyChunkSize, len(encoded))
		more := end < len(encoded)
		mode := 0
		if more {
			mode = 1
		}
		control := fmt.Sprintf("m=%d", mode)
		if start == 0 {
			if virtualPlacement {
				control = fmt.Sprintf("a=t,t=d,f=100,i=%d%s,q=2,%s", id, placement, control)
			} else {
				control = fmt.Sprintf("a=T,U=1,f=100,i=%d,c=%d,r=%d%s,q=2,%s", id, columns, rows, placement, control)
			}
		}
		out.WriteString("\x1b_G")
		out.WriteString(control)
		out.WriteByte(';')
		out.WriteString(encoded[start:end])
		out.WriteString("\x1b\\")
	}
	if virtualPlacement {
		fmt.Fprintf(&out, "\x1b_Ga=p,U=1,i=%d,c=%d,r=%d%s,q=2;\x1b\\", id, columns, rows, placement)
	}
	return out.String()
}

func kittyPlaceholder(id uint32, columns, rows int) string {
	return kittyPlaceholderWithPlacement(id, columns, rows, 0)
}

func kittyPlaceholderWithPlacement(id uint32, columns, rows int, placementID uint32) string {
	imageID := id & 0xFFFFFF
	red, green, blue := byte(imageID>>16), byte(imageID>>8), byte(imageID)
	placement := placementID & 0xFFFFFF
	placementRed, placementGreen, placementBlue := byte(placement>>16), byte(placement>>8), byte(placement)
	var out strings.Builder
	for row := 0; row < rows; row++ {
		if row > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "\x1b[38;2;%d;%d;%dm", red, green, blue)
		if placementID > 0 {
			fmt.Fprintf(&out, "\x1b[58;2;%d;%d;%dm", placementRed, placementGreen, placementBlue)
		}
		for column := 0; column < columns; column++ {
			out.WriteRune('\U0010EEEE')
			out.WriteRune(kittyDiacritic(uint32(row)))
			out.WriteRune(kittyDiacritic(uint32(column)))
			if high := id >> 24; high > 0 {
				out.WriteRune(kittyDiacritic(high))
			}
		}
		out.WriteString("\x1b[39m")
		if placementID > 0 {
			out.WriteString("\x1b[59m")
		}
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
