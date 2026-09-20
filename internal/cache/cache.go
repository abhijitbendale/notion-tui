// Package cache persists the last-fetched page list to disk so startup can
// render instantly while a background refresh runs.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"

	"notion-tui/internal/notion"
)

// Path returns the cache file location, honoring XDG_CACHE_HOME.
func Path() (string, error) {
	dir := os.Getenv("XDG_CACHE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".cache")
	}
	dir = filepath.Join(dir, "notion-tui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "pages.json"), nil
}

// Snapshot is the cached set of search results used to render the tree
// on startup before a fresh fetch completes.
type Snapshot struct {
	Pages       []notion.Page       `json:"pages"`
	DataSources []notion.DataSource `json:"data_sources"`
}

// Load reads the cached snapshot, if any. Returns a zero Snapshot, nil if no cache exists.
func Load() (Snapshot, error) {
	path, err := Path()
	if err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil
		}
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// Save writes the snapshot to the cache file.
func Save(snap Snapshot) error {
	path, err := Path()
	if err != nil {
		return err
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
