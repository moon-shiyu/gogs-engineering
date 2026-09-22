package bootstate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reclaimForTest(t *testing.T) []string {
	t.Helper()
	var lines []string
	Reclaim(func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	return lines
}

func TestReclaim_removesEmptyTrackedDirs(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	root := t.TempDir()
	created := filepath.Join(root, "created")
	nested := filepath.Join(created, "nested")
	require.NoError(t, os.MkdirAll(nested, 0o700))

	TrackDir(created)
	TrackDir(nested)

	lines := reclaimForTest(t)

	assert.NoDirExists(t, created)
	assert.NoDirExists(t, nested)
	require.NotEmpty(t, lines)
	assert.Contains(t, lines[0], "Removed")
}

func TestReclaim_keepsNonEmptyDirs(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	created := filepath.Join(t.TempDir(), "created")
	require.NoError(t, os.MkdirAll(created, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(created, "data.db"), []byte("x"), 0o600))

	TrackDir(created)

	lines := reclaimForTest(t)

	assert.DirExists(t, created)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "Kept")
}

func TestReclaim_keepsMarkedPathsWithReason(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	dir := t.TempDir()
	marker := filepath.Join(dir, "gogs-migrating")
	require.NoError(t, os.WriteFile(marker, []byte("from_version=21"), 0o600))

	TrackDir(dir)
	KeepOnFailure(marker, "migration did not finish")

	lines := reclaimForTest(t)

	assert.FileExists(t, marker)
	assert.DirExists(t, dir)
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], marker)
	assert.Contains(t, lines[0], "migration did not finish")
	assert.Contains(t, lines[1], "Kept")
}

func TestKeepsReturnsCopy(t *testing.T) {
	Reset()
	t.Cleanup(Reset)

	KeepOnFailure("/tmp/marker", "reason")
	got := Keeps()
	got["/tmp/marker"] = "mutated"
	delete(got, "/tmp/marker")

	assert.Equal(t, map[string]string{"/tmp/marker": "reason"}, Keeps())

	DropKeep("/tmp/marker")
	assert.Empty(t, Keeps())
}
