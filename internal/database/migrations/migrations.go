package migrations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"gorm.io/gorm"
	log "unknwon.dev/clog/v2"

	"gogs.io/gogs/internal/bootid"
	"gogs.io/gogs/internal/bootstate"
	"gogs.io/gogs/internal/conf"
)

const minDBVersion = 19

// markerName is the name of the marker file placed in the data directory
// while migrations are in flight. Its presence at startup means the previous
// run was interrupted before it could record completion.
const markerName = "gogs-migrating"

// markerPath returns the path of the migration marker file. It is a variable
// so tests can point it at a temporary directory. An empty return disables
// the marker mechanism, which only happens when the configuration has not
// been initialized (e.g., in unit tests of individual migrations).
var markerPath = func() string {
	if conf.Server.AppDataPath == "" {
		return ""
	}
	return filepath.Join(conf.Server.AppDataPath, markerName)
}

type Migration interface {
	Description() string
	Migrate(*gorm.DB) error
}

type migration struct {
	description string
	migrate     func(*gorm.DB) error
}

func NewMigration(desc string, fn func(*gorm.DB) error) Migration {
	return &migration{desc, fn}
}

func (m *migration) Description() string {
	return m.description
}

func (m *migration) Migrate(db *gorm.DB) error {
	return m.migrate(db)
}

// Version represents the version table. It should have only one row with `id == 1`.
type Version struct {
	ID      int64
	Version int64
}

// This is a sequence of migrations. Add new migrations to the bottom of the list.
// If you want to "retire" a migration, remove it from the top of the list and
// update _MIN_VER_DB accordingly
var migrations = []Migration{
	// v0 -> v4 : before 0.6.0 -> last support 0.7.33
	// v4 -> v10: before 0.7.0 -> last support 0.9.141
	// v10 -> v19: before 0.11.55 -> last support 0.12.0

	// Add new migration here, example:
	// v18 -> v19:v0.11.55
	// NewMigration("clean unlinked webhook and hook_tasks", cleanUnlinkedWebhookAndHookTasks),

	// v19 -> v20:v0.13.0
	NewMigration("migrate access tokens to store SHA56", migrateAccessTokenToSHA256),
	// v20 -> v21:v0.13.0
	NewMigration("add index to action.user_id", addIndexToActionUserID),
	// v21 -> v22:v0.13.0
	//
	// NOTE: There was a bug in calculating the value of the `version.version`
	// column after a migration is done, thus some instances are on v21 but some are
	// on v22. Let's make a noop v22 to make sure every instance will not miss a
	// real future migration.
	NewMigration("noop", func(*gorm.DB) error { return nil }),
}

var errMigrationSkipped = errors.New("the migration has been skipped")

// Migrate migrates the database schema and/or data to the current version.
func Migrate(db *gorm.DB) error {
	// NOTE: GORM has problem migrating tables that happen to have columns with the
	// same name, see https://github.com/gogs/gogs/issues/7056.
	if !db.Migrator().HasTable(new(Version)) {
		err := db.AutoMigrate(new(Version))
		if err != nil {
			return errors.Wrap(err, `auto migrate "version" table`)
		}
	}

	var current Version
	err := db.Where("id = ?", 1).First(&current).Error
	if err == gorm.ErrRecordNotFound {
		err = db.Create(
			&Version{
				ID:      1,
				Version: int64(minDBVersion + len(migrations)),
			},
		).Error
		if err != nil {
			return errors.Wrap(err, "create the version record")
		}
		return nil

	} else if err != nil {
		return errors.Wrap(err, "get the version record")
	}

	if minDBVersion > current.Version {
		log.Fatal(`
Hi there, thank you for using Gogs for so long!
However, Gogs has stopped supporting auto-migration from your previously installed version.
But the good news is, it's very easy to fix this problem!
You can migrate your older database using a previous release, then you can upgrade to the newest version.

Please save following instructions to somewhere and start working:

- If you were using below 0.6.0 (e.g. 0.5.x), download last supported archive from following link:
	https://github.com/gogs/gogs/releases/tag/v0.7.33
- If you were using below 0.7.0 (e.g. 0.6.x), download last supported archive from following link:
	https://github.com/gogs/gogs/releases/tag/v0.9.141
- If you were using below 0.11.55 (e.g. 0.9.141), download last supported archive from following link:
	https://github.com/gogs/gogs/releases/tag/v0.12.0

Once finished downloading:

1. Extract the archive and to upgrade steps as usual.
2. Run it once. To verify, you should see some migration traces.
3. Once it starts web server successfully, stop it.
4. Now it's time to put back the release archive you originally intent to upgrade.
5. Enjoy!

In case you're stilling getting this notice, go through instructions again until it disappears.`)
		return nil
	}

	target := int64(minDBVersion + len(migrations))

	if current.Version > target {
		// The database was migrated by a newer Gogs version and the user
		// rolled back to this one. Leave the version record untouched so a
		// later upgrade does not re-run migrations over existing data.
		log.Warn("Database version %d is newer than this Gogs version supports (%d), leaving the version record untouched", current.Version, target)
		return nil
	}

	marker := markerPath()
	if current.Version == target {
		// A crash between the final version bump and the marker cleanup
		// leaves a stale marker behind even though every migration ran.
		if marker != "" {
			if _, statErr := os.Stat(marker); statErr == nil {
				if removeErr := os.Remove(marker); removeErr != nil {
					return errors.Wrapf(removeErr, "remove stale migration marker %q", marker)
				}
				bootstate.DropKeep(marker)
				log.Info("Removed stale migration marker %q from a completed migration", marker)
			}
		}
		return nil
	}

	if marker != "" {
		if content, readErr := os.ReadFile(marker); readErr == nil {
			reason := fmt.Sprintf("a previous database migration did not finish (%s), the database may be partially migrated and needs manual inspection", strings.TrimSpace(string(content)))
			bootstate.KeepOnFailure(marker, reason)
			return errors.Newf(
				"a previous database migration did not finish, the database may be in a partially migrated state. Restore the database from a backup or inspect it manually, then remove the marker file %q to retry. The marker is kept in place to avoid re-running migrations over inconsistent data",
				marker,
			)
		}

		// No migrations have run for this attempt yet, guard the whole batch.
		if err = os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			return errors.Wrap(err, "create migration marker directory")
		}
		content := fmt.Sprintf("started_at=%s instance=%s from_version=%d target_version=%d",
			time.Now().UTC().Format(time.RFC3339), bootid.Instance(), current.Version, target)
		if err = os.WriteFile(marker, []byte(content), 0o600); err != nil {
			return errors.Wrap(err, "write migration marker")
		}
		bootstate.KeepOnFailure(marker, fmt.Sprintf("a database migration from version %d to %d did not finish, the database may be partially migrated and needs manual inspection", current.Version, target))
	}

	for _, m := range migrations[current.Version-minDBVersion:] {
		log.Info("Migration: %s", m.Description())
		if err = m.Migrate(db); err != nil {
			if err != errMigrationSkipped {
				return errors.Wrap(err, "do migrate")
			}
			log.Trace("The migration %q has been skipped", m.Description())
		}

		current.Version++
		err = db.Where("id = ?", current.ID).Updates(current).Error
		if err != nil {
			return errors.Wrap(err, "update the version record")
		}
	}

	if marker != "" {
		if err = os.Remove(marker); err != nil {
			return errors.Wrapf(err, "remove migration marker %q", marker)
		}
		bootstate.DropKeep(marker)
	}
	return nil
}
