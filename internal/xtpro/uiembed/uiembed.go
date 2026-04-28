package uiembed

import (
	"embed"
	"io/fs"
	"net/http"
)

// Static is the embedded filesystem containing the UI assets.
//
// The build script populates internal/xtpro/uiembed/static/ by copying from src/frontend/.
//go:embed static/**
var Static embed.FS

// Root returns an http.FileSystem rooted at the embedded "static" directory.
func Root() http.FileSystem {
	sub, err := fs.Sub(Static, "static")
	if err != nil {
		// Should never happen if the embed directive is correct.
		return http.FS(Static)
	}
	return http.FS(sub)
}

// Sub returns an http.FileSystem rooted at static/<dir>.
func Sub(dir string) (http.FileSystem, error) {
	root, err := fs.Sub(Static, "static")
	if err != nil {
		return nil, err
	}
	sub, err := fs.Sub(root, dir)
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

