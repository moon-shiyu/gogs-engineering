package migrations

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"gogs.io/gogs/internal/bootstate"
	"gogs.io/gogs/internal/dbtest"
)

// swapMigrations replaces the migration list and marker location for a test.
func swapMigrations(t *testing.T, ms []Migration) (marker string) {
	t.Helper()

	savedMigrations := migrations
	migrations = ms
	savedMarkerPath := markerPath
	marker = filepath.Join(t.TempDir(), markerName)
	markerPath = func() string { return marker }
	t.Cleanup(func() {
		migrations = savedMigrations
		markerPath = savedMarkerPath
	})
	return marker
}

func currentVersion(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var v Version
	require.NoError(t, db.Where("id = ?", 1).First(&v).Error)
	return v.Version
}

func TestMigrate_freshInstall(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	ran := 0
	marker := swapMigrations(t, []Migration{
		NewMigration("first", func(*gorm.DB) error { ran++; return nil }),
	})

	db := dbtest.NewDB(t, "migrateFreshInstall")
	require.NoError(t, Migrate(db))

	assert.Equal(t, int64(minDBVersion+1), currentVersion(t, db))
	assert.Equal(t, 0, ran, "fresh installs must not run migrations")
	assert.NoFileExists(t, marker)
}

func TestMigrate_runsPendingMigrationsOnce(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	ran := 0
	marker := swapMigrations(t, []Migration{
		NewMigration("first", func(*gorm.DB) error { ran++; return nil }),
		NewMigration("second", func(*gorm.DB) error { ran++; return nil }),
	})

	db := dbtest.NewDB(t, "migrateRunsPending", new(Version))
	require.NoError(t, db.Create(&Version{ID: 1, Version: minDBVersion}).Error)

	require.NoError(t, Migrate(db))
	assert.Equal(t, int64(minDBVersion+2), currentVersion(t, db))
	assert.Equal(t, 2, ran)
	assert.NoFileExists(t, marker, "marker must be removed after a successful migration")

	// A repeated start must not run the migrations again.
	require.NoError(t, Migrate(db))
	assert.Equal(t, 2, ran)
	assert.Equal(t, int64(minDBVersion+2), currentVersion(t, db))
}

func TestMigrate_interruptedMarkerStopsStartup(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	ran := 0
	marker := swapMigrations(t, []Migration{
		NewMigration("first", func(*gorm.DB) error { ran++; return nil }),
	})
	bootstate.Reset()
	t.Cleanup(bootstate.Reset)

	db := dbtest.NewDB(t, "migrateInterrupted", new(Version))
	require.NoError(t, db.Create(&Version{ID: 1, Version: minDBVersion}).Error)
	require.NoError(t, os.WriteFile(marker, []byte("from_version=19"), 0o600))

	err := Migrate(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not finish")
	assert.Contains(t, err.Error(), marker)
	assert.Equal(t, 0, ran, "no migration may run while an interrupted one is unresolved")
	assert.FileExists(t, marker, "the marker must be kept for manual handling")
	assert.Contains(t, bootstate.Keeps(), marker)
	assert.Equal(t, int64(minDBVersion), currentVersion(t, db), "version record must stay put")
}

func TestMigrate_staleMarkerFromCompletedMigration(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	marker := swapMigrations(t, []Migration{
		NewMigration("first", func(*gorm.DB) error { return nil }),
	})

	db := dbtest.NewDB(t, "migrateStaleMarker", new(Version))
	require.NoError(t, db.Create(&Version{ID: 1, Version: minDBVersion + 1}).Error)
	require.NoError(t, os.WriteFile(marker, []byte("from_version=19"), 0o600))

	require.NoError(t, Migrate(db))
	assert.NoFileExists(t, marker, "a stale marker from a completed migration is reclaimed")
}

func TestMigrate_failedMigrationKeepsMarker(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	marker := swapMigrations(t, []Migration{
		NewMigration("boom", func(*gorm.DB) error { return errors.New("disk gone") }),
	})
	bootstate.Reset()
	t.Cleanup(bootstate.Reset)

	db := dbtest.NewDB(t, "migrateFailed", new(Version))
	require.NoError(t, db.Create(&Version{ID: 1, Version: minDBVersion}).Error)

	err := Migrate(db)
	require.Error(t, err)
	assert.FileExists(t, marker, "the marker stays behind to flag the half-migrated state")
	assert.Contains(t, bootstate.Keeps(), marker)
}

func TestMigrate_downgradeLeavesVersionUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	swapMigrations(t, []Migration{
		NewMigration("first", func(*gorm.DB) error { return nil }),
	})

	db := dbtest.NewDB(t, "migrateDowngrade", new(Version))
	future := int64(minDBVersion + 10)
	require.NoError(t, db.Create(&Version{ID: 1, Version: future}).Error)

	require.NoError(t, Migrate(db))
	assert.Equal(t, future, currentVersion(t, db), "rolling back must not rewrite the version record")
}
