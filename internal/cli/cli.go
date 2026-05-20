// Package cli is the root of the Cobra command tree for the `prexel` binary.
// Both server (`prexel serve`) and client subcommands live under this tree.
package cli

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/prexel/prexel/internal/cli/command"
)

// Assets bundles the embedded filesystems that main.go materialises and hands
// to the CLI. Keeping the embed in main.go avoids leaking embed semantics into
// library packages.
type Assets struct {
	Migrations fs.FS
	WebDist    fs.FS
}

// Execute runs the root command and exits on error.
func Execute(a Assets) {
	root := command.NewRoot(command.Assets{
		Migrations: a.Migrations,
		WebDist:    a.WebDist,
	})
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
