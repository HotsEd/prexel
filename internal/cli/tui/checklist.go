package tui

import (
	"fmt"
	"os"
)

// Check is one named step in a checklist.
type Check struct {
	Name string
	Fn   func() (ok bool, message string)
}

// RunChecklist runs each check sequentially and prints ✓/✗ inline. Returns
// nil if all checks passed; otherwise an aggregate error.
func RunChecklist(title string, checks []Check) error {
	if title != "" {
		fmt.Fprintln(os.Stderr, title)
	}
	allOK := true
	for _, c := range checks {
		fmt.Fprintf(os.Stderr, "  ⋯ %s\n", c.Name)
		ok, msg := c.Fn()
		// Move cursor up + clear line, then print final.
		if IsTTY() {
			fmt.Fprint(os.Stderr, "\033[1A\r\033[2K")
		}
		marker := "✓"
		if !ok {
			marker = "✗"
			allOK = false
		}
		if msg != "" {
			fmt.Fprintf(os.Stderr, "  %s %s — %s\n", marker, c.Name, msg)
		} else {
			fmt.Fprintf(os.Stderr, "  %s %s\n", marker, c.Name)
		}
	}
	if !allOK {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

// PrintReport renders a slice of (name, ok, message) tuples as a checklist.
func PrintReport(title string, items []ReportRow) {
	if title != "" {
		fmt.Fprintln(os.Stderr, title)
	}
	for _, it := range items {
		marker := "✓"
		if !it.OK {
			marker = "✗"
		}
		if it.Message != "" {
			fmt.Fprintf(os.Stderr, "  %s %s — %s\n", marker, it.Name, it.Message)
		} else {
			fmt.Fprintf(os.Stderr, "  %s %s\n", marker, it.Name)
		}
	}
}

// ReportRow is a single row for PrintReport.
type ReportRow struct {
	Name    string
	OK      bool
	Message string
}
