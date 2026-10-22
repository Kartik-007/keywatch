// Package ui holds shared terminal styles and prompts so every command looks
// and asks the same way.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

var (
	Bold   = lipgloss.NewStyle().Bold(true)
	Dim    = lipgloss.NewStyle().Faint(true)
	Red    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	Yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	Green  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	Cmd    = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

// Confirm asks a yes/no question. defYes picks the default on Enter.
// Non-interactive stdin always answers the default.
func Confirm(q string, defYes bool) bool {
	hint := "[y/N]"
	if defYes {
		hint = "[Y/n]"
	}
	fmt.Printf("  %s %s ", q, hint)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Println()
		return defYes
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defYes
	}
	return line == "y" || line == "yes"
}

// Ask reads one line of plain text.
func Ask(q string) string {
	fmt.Printf("  %s ", q)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}

// AskSecret reads a line without echoing it.
func AskSecret(q string) (string, error) {
	fmt.Printf("  %s ", q)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println("••••••••")
	return strings.TrimSpace(string(b)), err
}

// Receipt is the last line of every run: what was read, what was changed.
func Receipt(readFrom []string, changed []string) string {
	read := "nothing left this machine"
	if len(readFrom) > 0 {
		read = "Read from: " + strings.Join(readFrom, ", ")
	}
	if len(changed) == 0 {
		return Dim.Render("  Nothing was changed. " + read + ".")
	}
	return Dim.Render("  Changed: " + strings.Join(changed, ", ") + ". " + read + ".")
}
