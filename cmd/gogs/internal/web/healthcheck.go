package web

import (
	stdctx "context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	log "unknwon.dev/clog/v2"

	"gogs.io/gogs/internal/bootid"
	"gogs.io/gogs/internal/conf"
	"gogs.io/gogs/internal/database"
)

// healthCheckProbe is a single named dependency check.
type healthCheckProbe struct {
	name string
	err  error
}

// healthCheckTimeout bounds each database probe so a hanging database cannot
// stall the health check endpoint itself.
const healthCheckTimeout = 3 * time.Second

// healthCheckDirs returns the key directories the instance must be able to
// write to in order to serve traffic.
func healthCheckDirs() []struct {
	name string
	path string
} {
	dirs := []struct {
		name string
		path string
	}{
		{"data directory", conf.Server.AppDataPath},
		{"repository root", conf.Repository.Root},
	}
	if conf.Log != nil && slices.Contains(conf.Log.Modes, "file") {
		dirs = append(dirs, struct {
			name string
			path string
		}{"log directory", conf.Log.RootPath})
	}
	if conf.Repository.Upload.Enabled {
		dirs = append(dirs, struct {
			name string
			path string
		}{"upload temp directory", conf.Repository.Upload.TempPath})
	}
	if conf.LFS.Storage == "local" {
		dirs = append(dirs, struct {
			name string
			path string
		}{"LFS objects directory", conf.LFS.ObjectsPath})
	}
	return dirs
}

// ensureWritableDir verifies the directory accepts new files, creating it
// first like the application would on first use.
func ensureWritableDir(path string) error {
	if path == "" {
		return errors.New("path is not configured")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errors.Wrap(err, "create directory")
	}
	probe, err := os.CreateTemp(path, ".healthcheck-*")
	if err != nil {
		return errors.Wrap(err, "create probe file")
	}
	name := probe.Name()
	_ = probe.Close()
	if err := os.Remove(name); err != nil {
		return errors.Wrap(err, "remove probe file")
	}
	return nil
}

// runHealthChecks probes every dependency live on each call, so a recovered
// dependency flips the instance back to healthy without any restart. It is a
// variable so tests can substitute probes.
var runHealthChecks = func() []healthCheckProbe {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), healthCheckTimeout)
	dbErr := database.PingContext(ctx)
	cancel()

	probes := []healthCheckProbe{{name: "database connection", err: dbErr}}
	for _, dir := range healthCheckDirs() {
		probes = append(probes, healthCheckProbe{name: dir.name, err: ensureWritableDir(dir.path)})
	}
	return probes
}

func healthCheck(w http.ResponseWriter, r *http.Request) {
	probes := runHealthChecks()

	healthy := true
	for _, probe := range probes {
		if probe.err != nil {
			healthy = false
			break
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if healthy {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		for _, probe := range probes {
			if probe.err != nil {
				log.Warn("Health check failed (instance %s): %s: %v", bootid.Instance(), probe.name, probe.err)
			}
		}
	}
	if r.Method == http.MethodHead {
		return
	}
	_, _ = fmt.Fprintf(w, "* Instance: %s\n", bootid.Instance())
	for _, probe := range probes {
		if probe.err != nil {
			_, _ = fmt.Fprintf(w, "* %s: %v\n", probe.name, probe.err)
		} else {
			_, _ = fmt.Fprintf(w, "* %s: OK\n", probe.name)
		}
	}
}
