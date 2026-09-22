package conf

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cockroachdb/errors"

	"gogs.io/gogs/internal/bootstate"
	"gogs.io/gogs/internal/osx"
	"gogs.io/gogs/internal/process"
)

// cleanUpOpenSSHVersion cleans up the raw output of "ssh -V" and returns a clean version string.
func cleanUpOpenSSHVersion(raw string) string {
	v := strings.TrimRight(strings.Fields(raw)[0], ",1234567890")
	v = strings.TrimSuffix(strings.TrimPrefix(v, "OpenSSH_"), "p")
	return v
}

// openSSHVersion returns string representation of OpenSSH version via command "ssh -V".
func openSSHVersion() (string, error) {
	// NOTE: Somehow the version is printed to stderr.
	_, stderr, err := process.Exec("conf.openSSHVersion", "ssh", "-V")
	if err != nil {
		return "", errors.Wrap(err, stderr)
	}

	return cleanUpOpenSSHVersion(stderr), nil
}

// ensureAbs prepends the WorkDir to the given path if it is not an absolute path.
func ensureAbs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(WorkDir(), path)
}

// mkdirAllTracked creates the directory like os.MkdirAll and, when the
// directory did not exist before, records it so a failed startup can reclaim
// it again.
func mkdirAllTracked(path string, perm os.FileMode) error {
	existed := osx.IsDir(path)
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	if !existed {
		bootstate.TrackDir(path)
	}
	return nil
}

// CheckRunUser returns false if configured run user does not match actual user that
// runs the app. The first return value is the actual user name. This check is ignored
// under Windows since SSH remote login is not the main method to login on Windows.
func CheckRunUser(runUser string) (string, bool) {
	if IsWindowsRuntime() {
		return "", true
	}

	currentUser := osx.CurrentUsername()
	return currentUser, runUser == currentUser
}
