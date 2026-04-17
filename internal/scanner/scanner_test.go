package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestFindStaleCandidates(t *testing.T) {
	localRoot := t.TempDir()

	mustWriteFile(t, filepath.Join(localRoot, "existing.txt"), "ok")
	mustWriteFile(t, filepath.Join(localRoot, "nested", "exists2.txt"), "ok")

	now := time.Now()
	s3Objects := map[string]S3Object{
		"existing.txt": {
			Key:          "existing.txt",
			Size:         100,
			LastModified: now.Add(-365 * 24 * time.Hour),
		},
		"nested/exists2.txt": {
			Key:          "nested/exists2.txt",
			Size:         200,
			LastModified: now.Add(-365 * 24 * time.Hour),
		},
		"missing-old.txt": {
			Key:          "missing-old.txt",
			Size:         300,
			LastModified: now.Add(-365 * 24 * time.Hour),
		},
		"missing-new.txt": {
			Key:          "missing-new.txt",
			Size:         400,
			LastModified: now.Add(-1 * time.Hour),
		},
	}

	candidates := FindStaleCandidates(s3Objects, localRoot, 24*time.Hour)
	gotKeys := make([]string, 0, len(candidates))
	for _, c := range candidates {
		gotKeys = append(gotKeys, c.Key)
	}
	sort.Strings(gotKeys)

	wantKeys := []string{"missing-old.txt"}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("candidate count = %d (%v), want %d (%v)", len(gotKeys), gotKeys, len(wantKeys), wantKeys)
	}
	for i := range wantKeys {
		if gotKeys[i] != wantKeys[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, gotKeys[i], wantKeys[i])
		}
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
