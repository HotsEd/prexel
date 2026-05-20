package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Select prompts the user to pick one of `options`. `render` formats each
// option for display. Returns the index of the chosen option.
//
// In a non-TTY environment, returns an error.
func Select[T any](title string, options []T, render func(T) string) (int, T, error) {
	var zero T
	if len(options) == 0 {
		return -1, zero, fmt.Errorf("no options to choose from")
	}
	if !IsStdinTTY() {
		return -1, zero, fmt.Errorf("Select requires a TTY")
	}
	fmt.Fprintln(os.Stderr, title)
	for i, opt := range options {
		fmt.Fprintf(os.Stderr, "  %d) %s\n", i+1, render(opt))
	}
	for {
		ans, err := Prompt(fmt.Sprintf("Choose [1-%d]: ", len(options)))
		if err != nil {
			return -1, zero, err
		}
		ans = strings.TrimSpace(ans)
		n, err := strconv.Atoi(ans)
		if err != nil || n < 1 || n > len(options) {
			fmt.Fprintln(os.Stderr, "invalid selection, try again")
			continue
		}
		return n - 1, options[n-1], nil
	}
}
