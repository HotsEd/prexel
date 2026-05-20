package main

import (
	"embed"
	"io/fs"

	"github.com/prexel/prexel/internal/cli"
)

//go:embed all:migrations
var migrationsFS embed.FS

// web/dist is only present in production builds (the Dockerfile builds the
// frontend before invoking `go build`). In dev, the directory exists with a
// placeholder index.html so this embed never fails.
//
//go:embed all:web/dist
var webDistFS embed.FS

func main() {
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		panic(err)
	}
	webDist, err := fs.Sub(webDistFS, "web/dist")
	if err != nil {
		panic(err)
	}
	cli.Execute(cli.Assets{
		Migrations: migrations,
		WebDist:    webDist,
	})
}
