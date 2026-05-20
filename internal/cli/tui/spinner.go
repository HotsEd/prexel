package tui

import (
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// RunSpinner shows a simple spinner while `fn` runs. The spinner is suppressed
// in non-TTY environments — fn's result is still propagated. The label is
// printed exactly once on each render.
func RunSpinner(label string, fn func() error) error {
	if !IsTTY() {
		fmt.Fprintln(os.Stderr, label+" ...")
		return fn()
	}
	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	stop := make(chan struct{})
	done := make(chan struct{})
	var idx atomic.Int32
	go func() {
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				i := int(idx.Add(1)) % len(frames)
				fmt.Fprintf(os.Stderr, "\r%c %s", frames[i], label)
			}
		}
	}()
	err := fn()
	close(stop)
	<-done
	// Clear line, print final state.
	fmt.Fprint(os.Stderr, "\r\033[2K")
	if err != nil {
		fmt.Fprintln(os.Stderr, "✗ "+label)
	} else {
		fmt.Fprintln(os.Stderr, "✓ "+label)
	}
	return err
}

// Println writes a line to stderr unconditionally — convenience for keeping
// human messages out of stdout when the command is parsed by scripts.
func Println(w io.Writer, args ...any) {
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintln(w, args...)
}
