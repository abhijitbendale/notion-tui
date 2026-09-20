package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"notion-tui/internal/ui"
)

func main() {
	if _, err := exec.LookPath("ntn"); err != nil {
		fmt.Fprintln(os.Stderr, "notion-tui: `ntn` CLI not found on PATH. Install and log in first (ntn login).")
		os.Exit(1)
	}

	p := tea.NewProgram(ui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "notion-tui: ", err)
		os.Exit(1)
	}
}
