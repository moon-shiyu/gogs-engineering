package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gogs.io/gogs/internal/conf"
)

func swapRunHealthChecks(t *testing.T, probes []healthCheckProbe) {
	t.Helper()
	saved := runHealthChecks
	runHealthChecks = func() []healthCheckProbe { return probes }
	t.Cleanup(func() { runHealthChecks = saved })
}

func TestHealthCheck_healthy(t *testing.T) {
	swapRunHealthChecks(t, []healthCheckProbe{
		{name: "database connection"},
		{name: "data directory"},
	})

	recorder := httptest.NewRecorder()
	healthCheck(recorder, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "* database connection: OK")
	assert.Contains(t, recorder.Body.String(), "* data directory: OK")
	assert.Contains(t, recorder.Body.String(), "* Instance: ")
}

func TestHealthCheck_unhealthy(t *testing.T) {
	swapRunHealthChecks(t, []healthCheckProbe{
		{name: "database connection", err: errors.New("connection refused")},
		{name: "data directory"},
	})

	recorder := httptest.NewRecorder()
	healthCheck(recorder, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "connection refused")
	assert.Contains(t, recorder.Body.String(), "* data directory: OK")
}

func TestHealthCheck_headReturnsStatusOnly(t *testing.T) {
	swapRunHealthChecks(t, []healthCheckProbe{
		{name: "database connection", err: errors.New("connection refused")},
	})

	recorder := httptest.NewRecorder()
	healthCheck(recorder, httptest.NewRequest(http.MethodHead, "/healthcheck", nil))

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Empty(t, recorder.Body.String())
}

func TestEnsureWritableDir(t *testing.T) {
	t.Run("existing writable dir", func(t *testing.T) {
		assert.NoError(t, ensureWritableDir(t.TempDir()))
	})

	t.Run("missing dir is created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "nested", "dir")
		require.NoError(t, ensureWritableDir(path))
		assert.DirExists(t, path)
	})

	t.Run("empty path", func(t *testing.T) {
		assert.Error(t, ensureWritableDir(""))
	})

	t.Run("path is a file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
		assert.Error(t, ensureWritableDir(file))
	})
}

func TestHealthCheckDirs(t *testing.T) {
	savedServer, savedRepository, savedLFS, savedLog := conf.Server, conf.Repository, conf.LFS, conf.Log
	t.Cleanup(func() {
		conf.Server, conf.Repository, conf.LFS, conf.Log = savedServer, savedRepository, savedLFS, savedLog
	})

	conf.Server.AppDataPath = "/data"
	conf.Repository.Root = "/repos"
	conf.Repository.Upload.Enabled = true
	conf.Repository.Upload.TempPath = "/tmp/uploads"
	conf.LFS.Storage = "local"
	conf.LFS.ObjectsPath = "/lfs"
	conf.Log = nil

	names := make([]string, 0)
	for _, dir := range healthCheckDirs() {
		names = append(names, dir.name)
	}
	assert.Equal(t, []string{"data directory", "repository root", "upload temp directory", "LFS objects directory"}, names)
}
