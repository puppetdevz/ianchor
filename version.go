// Package ianchor exposes build metadata for the ianchor CLI.
package ianchor

import (
	_ "embed"
	"strings"
)

// versionFile is the single source of truth for the released version. The
// release workflow reads VERSION, so this Go constant is derived from it at
// build time rather than being maintained twice.
//
//go:embed VERSION
var versionFile string

// Version is the ianchor version reported by `ianchor --version`.
var Version = strings.TrimSpace(versionFile)
