package anchor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/puppetdevz/ianchor/internal/config"
	"github.com/puppetdevz/ianchor/internal/known"
	"github.com/puppetdevz/ianchor/internal/store"
)

var ignoredNames = map[string]bool{
	".DS_Store": true,
	"Thumbs.db": true,
	".bak":      true,
}

var imageExtensions = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".svg": true, ".ico": true, ".webp": true, ".tiff": true, ".tif": true,
	".heic": true, ".heif": true,
}

func ignored(name string) bool {
	if ignoredNames[name] {
		return true
	}
	return imageExtensions[strings.ToLower(filepath.Ext(name))]
}

// InitResult describes a completed initialisation.
type InitResult struct {
	ConfigDir  string
	Discovered int
	Entries    []store.Entry
}

// Init creates the iCloud config directory, opens the database and records any
// config files that were moved there before ianchor knew about them.
func Init(icloudBase, dirName string) (InitResult, error) {
	configDir, err := config.EnsureConfigDir(icloudBase, dirName)
	if err != nil {
		return InitResult{}, err
	}
	db, err := store.Open(configDir)
	if err != nil {
		return InitResult{}, err
	}
	defer db.Close()

	entries, err := discover(db, configDir)
	if err != nil {
		return InitResult{}, err
	}
	all, err := db.AllEntries()
	if err != nil {
		return InitResult{}, err
	}
	return InitResult{ConfigDir: configDir, Discovered: entries, Entries: all}, nil
}

func discover(db *store.DB, configDir string) (int, error) {
	dirEntries, err := os.ReadDir(configDir)
	if err != nil {
		return 0, err
	}
	var candidates []store.DiscoveredEntry
	for _, dirEntry := range dirEntries {
		name := dirEntry.Name()
		if strings.HasPrefix(name, "ianchor.db") || ignored(name) {
			continue
		}
		existing, err := db.GetEntry(name)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			continue
		}
		kind := "file"
		if info, err := os.Lstat(filepath.Join(configDir, name)); err == nil && info.IsDir() {
			kind = "directory"
		}
		// Invoke the injection hook before reserving a database connection; a
		// hook may itself use this handle, whose pool has only one connection.
		if err := checkOp("db.add", name); err != nil {
			return 0, err
		}
		candidates = append(candidates, store.DiscoveredEntry{Name: name, OriginalPath: known.Guess(name), Type: kind})
	}
	return db.AddDiscovered(candidates)
}
