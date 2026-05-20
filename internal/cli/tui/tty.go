// Package tui provides small terminal-UI helpers used by the CLI subcommands.
//
// Design notes: full-screen Bubbletea programs are only used where they really
// shine (the deploy --watch view). Everything else is a thin text adapter that
// degrades cleanly when stdout/stdin is not a TTY (CI, pipelines, `make ctl`).
package tui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"golang.org/x/term"
)

// IsTTY reports whether stdout is connected to a real terminal.
func IsTTY() bool {
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// IsStdinTTY reports whether stdin is interactive.
func IsStdinTTY() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// Prompt asks for a single line of input. The label is printed verbatim.
// Returns the trimmed answer. If stdin is not interactive, returns "" and a
// non-nil error.
func Prompt(label string) (string, error) {
	if !IsStdinTTY() {
		return "", fmt.Errorf("interactive prompt %q requires a TTY", label)
	}
	fmt.Fprint(os.Stderr, label)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// PromptDefault prompts with a default value shown in brackets. Empty answer
// returns the default.
func PromptDefault(label, def string) (string, error) {
	ans, err := Prompt(fmt.Sprintf("%s [%s]: ", label, def))
	if err != nil {
		return "", err
	}
	if ans == "" {
		return def, nil
	}
	return ans, nil
}

// PromptPassword reads a password from stdin without echoing.
func PromptPassword(label string) (string, error) {
	if !IsStdinTTY() {
		return "", fmt.Errorf("password prompt requires a TTY")
	}
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Confirm prompts for yes/no. Returns false on EOF / non-TTY by default.
func Confirm(question string) bool {
	if !IsStdinTTY() {
		return false
	}
	ans, err := Prompt(question + " [y/N]: ")
	if err != nil {
		return false
	}
	ans = strings.ToLower(strings.TrimSpace(ans))
	return ans == "y" || ans == "yes"
}

// ConfirmDefaultYes is like Confirm but defaults to yes on empty answer.
func ConfirmDefaultYes(question string) bool {
	if !IsStdinTTY() {
		return true
	}
	ans, err := Prompt(question + " [Y/n]: ")
	if err != nil {
		return true
	}
	ans = strings.ToLower(strings.TrimSpace(ans))
	if ans == "" {
		return true
	}
	return ans == "y" || ans == "yes"
}
