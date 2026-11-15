package anchor

import (
	"os"
	"path/filepath"

	"github.com/puppetdevz/ianchor/internal/fsx"
	"github.com/puppetdevz/ianchor/internal/store"
)

// List returns every tracked entry with its live status resolved against the
// filesystem, like `ianchor list` does.
func List(db *store.DB, configDir string) ([]store.Entry, error) {
	entries, err := db.AllEntries()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		entries[i].Status = liveStatus(entries[i], configDir)
	}
	return entries, nil
}

// liveStatus derives linked/unlinked/deleted without writing to the database.
func liveStatus(entry store.Entry, configDir string) string {
	if configDir == "" {
		return entry.Status
	}
	icloudPath := filepath.Join(configDir, entry.Name)
	if !fsx.Exists(icloudPath) {
		return "deleted"
	}
	if entry.OriginalPath == nil {
		return "unlinked"
	}
	original := *entry.OriginalPath
	if !fsx.IsSymlink(original) {
		return "unlinked"
	}
	target, err := os.Readlink(original)
	if err != nil || target != icloudPath {
		return "unlinked"
	}
	return "linked"
}
