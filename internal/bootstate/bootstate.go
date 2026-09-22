// Package bootstate tracks filesystem entries created while the application
// boots so a failed startup can reclaim what it created and preserve what
// needs manual attention.
//
// The registry is process-global because the entries are created deep inside
// configuration, logging, and database initialization, long before a
// structured cleanup value could be threaded through.
package bootstate

import (
	"os"
	"sort"
	"sync"
)

var (
	mu    sync.Mutex
	dirs  []string
	keeps = map[string]string{}
)

// TrackDir records a directory that was created during startup. Only
// directories that did not exist before the startup began should be tracked.
func TrackDir(path string) {
	mu.Lock()
	defer mu.Unlock()
	dirs = append(dirs, path)
}

// KeepOnFailure marks a path to be preserved when a failed startup reclaims
// what it created. The reason is shown to operators and must explain why the
// entry needs manual handling.
func KeepOnFailure(path, reason string) {
	mu.Lock()
	defer mu.Unlock()
	keeps[path] = reason
}

// DropKeep removes a keep marker previously registered with KeepOnFailure,
// e.g., after the guarded operation completed successfully.
func DropKeep(path string) {
	mu.Lock()
	defer mu.Unlock()
	delete(keeps, path)
}

// Keeps returns a copy of the currently registered keep markers.
func Keeps() map[string]string {
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]string, len(keeps))
	for path, reason := range keeps {
		out[path] = reason
	}
	return out
}

// Reclaim removes tracked directories that are still empty, deepest first so
// nested creations collapse into a single leftover. Kept paths are never
// removed; their reasons are reported through logf instead. Entries that
// cannot be removed (e.g., they already contain files) are reported and left
// in place.
func Reclaim(logf func(format string, args ...any)) {
	mu.Lock()
	defer mu.Unlock()

	paths := make([]string, 0, len(keeps))
	for path := range keeps {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		logf("Kept %q for manual handling: %s", path, keeps[path])
	}

	ordered := make([]string, len(dirs))
	copy(ordered, dirs)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, dir := range ordered {
		if _, keep := keeps[dir]; keep {
			continue
		}
		err := os.Remove(dir)
		if err == nil {
			logf("Removed %q created during the failed startup", dir)
			continue
		}
		if os.IsNotExist(err) {
			continue
		}
		logf("Kept %q created during the failed startup: %v", dir, err)
	}
}

// Reset clears all tracked state. It is intended for tests.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	dirs = nil
	keeps = map[string]string{}
}
