package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	appName      = "m2z"
	permission   = "read:account,write:notes"
	requestLimit = 30
	menuWidth    = 18
)

type screen uint8

const (
	setupScreen screen = iota
	authScreen
	mainScreen
)

type focus uint8

const (
	menuFocus focus = iota
	contentFocus
	composerFocus
)

func main() {
	cfg, err := loadConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "設定の読み込みに失敗しました:", err)
		os.Exit(1)
	}

	m := newModel(cfg)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
