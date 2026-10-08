package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveAndLoadPersistsMetadataAtomically(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	before := time.Now().Add(-time.Second)
	if err := Save(Snapshot{}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != currentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", currentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.CachedAt.Before(before) {
		t.Fatalf("expected cache timestamp after %v, got %v", before, snapshot.CachedAt)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "notion-tui", "pages.json")); err != nil {
		t.Fatalf("expected cache file: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "notion-tui", ".pages.json-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary cache files were left behind: %v", matches)
	}
}

func TestLoadRejectsFutureSchema(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected future cache schema to be rejected")
	}
}
