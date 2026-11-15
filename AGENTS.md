# AGENTS.md

Project instructions for coding agents working in this repository.

## Project Overview

**iAnchor** (`github.com/puppetdevz/ianchor`) is a Go CLI tool that syncs dotfiles and app configs across Macs via iCloud Drive. It moves config files into iCloud Drive and creates symlinks at their original locations so apps still find them.

## Commands

```bash
go build ./...                                   # Build every package
go run ./cmd/ianchor --help                      # Run the CLI
GOTOOLCHAIN=local CGO_ENABLED=0 go test ./...    # Run all tests
GOTOOLCHAIN=local CGO_ENABLED=0 go test ./internal/anchor/  # Run one package
go vet ./...                                     # Vet
CGO_ENABLED=1 go test -race ./...                # Race detector
go test -coverprofile=/tmp/cover.out ./...       # Statement coverage
go tool cover -func=/tmp/cover.out | tail -1     # Total coverage (must print 100.0%)
gofmt -l .                                       # Must print nothing
```

Toolchain: Go 1.27.0, no cgo for the product (`CGO_ENABLED=0`); `-race` needs cgo and is test-only. Entry point: `cmd/ianchor/main.go`. The version comes from the embedded `VERSION` file (`version.go`), which is the single source of truth.

## Architecture

**CLI layer** (`internal/cli/`): A dependency-free port of the commander.js semantics the Node version used (option parsing, help layout, `error:` messages, suggestions). `internal/app/` declares the command tree and the user-facing output.

**Core modules** (`internal/`):

- `config/` — iCloud Drive path detection, `~/.config/ianchor.json`, config directory validation
- `store/` — SQLite access via `database/sql` + `modernc.org/sqlite`, schema and queries for `entries`
- `anchor/` — `init`, `add`, `recover`, `relink`, `remove`, `list`, plus the undo journal and `.bak` handling
- `fsx/` — filesystem helpers: copy/move with symlink and permission fidelity, staging paths
- `table/` + `textwidth/` — byte-compatible reimplementation of cli-table3's default table (ANSI colors, CJK widths)
- `known/` — maps common config filenames to their expected home paths
- `completions/` — embeds `internal/completions/assets/ianchor.{bash,zsh}` verbatim

**Data flow**: `main` → `cli.Run` parses argv into a command → `app` handler opens the DB and calls an `anchor` operation → the operation mutates the filesystem inside a journal, verifies, commits, and the handler prints the result.

**Database**: Single SQLite file (`ianchor.db`) inside the iCloud config directory. One table `entries` with columns `id`, `name` (unique), `original_path`, `type`, `status`, `created_at`, `updated_at`. The schema is unchanged from the Node version and there is no migration step.

## Testing

- Unit tests live next to the code (`internal/<pkg>/*_test.go`).
- `internal/app/compat_test.go` replays `internal/app/testdata/compat.json`: outputs, exit codes, filesystem tree and DB rows captured from the Node implementation. It is the compatibility contract — do not edit the fixture to make a mismatch pass; explain the intended delta in the implementation doc first.
- `internal/anchor/injection_test.go` injects a failure at every destructive operation and requires full rollback.
- `internal/completions/pty_darwin_test.go` drives real zsh/bash through a PTY.
- `internal/release/publish_test.go` runs `scripts/release/publish.sh` against a local bare tap repository with mocked `gh`/`curl`.
- Statement coverage must stay at 100% for every package that has statements; `version.go` and `internal/release` report `[no statements]`. `scripts/coverage-check.sh <profile>` is the gate: it fails on any function below `100.0%` and on any single uncovered statement, so the rounded `total:` line of `go tool cover -func` cannot hide a gap. Error, identity-check and cleanup branches are reached with OS-level setups where possible and with per-call seams where a real race or permission failure cannot be produced. Every seam is a package-level variable whose default is the real call, so shipped behaviour is unchanged; they live in `internal/fsx/hooks.go`, `internal/anchor/hooks.go`, `internal/cli/terminal_guard.go` (`setTerminalMode`, `watchSignals`, `signalProcess`, `watchResize`), `internal/cli/escape.go` (`pollEscapeInput`), `internal/anchor/ops.go` (`copyNodeImpl`), `internal/anchor/backup_root.go` (`copyIntoBackup`, `copyNodeModesIntoBackup`), `internal/fsx/fsx.go` (`randomReader`, `workingDirectory`) and `internal/config/config.go` (home-directory and parent-directory probes). `TestCopyFault` and `TestPermissionFault` in `internal/fsx/hooks.go` are exported only so `anchor` tests can interrupt a cross-package copy; they are always nil in production. `internal/fsx/seams_test.go` and `internal/anchor/seams_default_test.go` assert that the default of every seam still is the real call, so a seam cannot quietly become a stub.
- Tests that need a rejected permission change rely on real modes, so they call `requireUnprivileged` and skip under root. Darwin-only fixtures (`unix.Chflags`) have `_darwin_test.go`/`_other_test.go` pairs, so `go build`/`go vet ./internal/{fsx,anchor,store,config,known,table,textwidth,release}` succeed on other platforms. `internal/cli` is Darwin-only by design: `terminal_guard.go` uses termios ioctls (`TIOCGETA`/`TIOCSETA`) and `escape.go` uses `unix.Poll`; everything that imports it (`internal/app`, `cmd/ianchor`) inherits that limit, and `internal/completions` drives a real PTY. So `GOOS=linux go build ./...` fails at `internal/cli` — that is expected, not a regression. The supported product targets are darwin/amd64 and darwin/arm64 only.
- Acceptance scripts are opt-in: `scripts/acceptance/sqlite-interop.py` (legacy `better-sqlite3` interoperability) and `scripts/acceptance/cli-compat.py` (byte/stream/exit differential against baseline `941d9336f1c1b097aa752519ead6af5e009d4664`). Both need a prepared legacy checkout and never touch real iCloud data.

## Key Conventions

- Standard library first; `modernc.org/sqlite` and `golang.org/x/sys` are the only dependencies.
- The product binary must build with `CGO_ENABLED=0` for darwin/amd64 and darwin/arm64.
- User-visible strings are contract: keep exact wording, exit codes and stream selection (stdout vs stderr) when touching CLI or operation code.
- Destructive filesystem steps go through the `anchor` journal so a failure restores the previous state.
- Distribution via Homebrew (tap repository: `puppetdevz/homebrew-tap`, tap name: `puppetdevz/tap`); release via `scripts/tag.sh` + GitHub Actions. `Formula/ianchor.rb` in the source repository is the authoritative placeholder template (not directly installable); it builds from source with `depends_on "go" => :build`. The release workflow runs `scripts/release/publish.sh`, which renders the template from a stable tagged commit, creates or updates only `Formula/ianchor.rb` on the initialized personal tap's `main`, refuses downgrades, and skips identical reruns. `VERSION` must match the tag; tags are immutable. Maintainers must prepare the tap and `HOMEBREW_TAP_TOKEN`; real publishing requires explicit release authorization.