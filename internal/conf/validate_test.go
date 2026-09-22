package conf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swapGlobals replaces the global configuration structs for the duration of
// a test.
func swapGlobals(t *testing.T) {
	t.Helper()
	savedServer, savedDatabase, savedRepository := Server, Database, Repository
	savedSecurity, savedEmail, savedSession := Security, Email, Session
	savedCache, savedLFS := Cache, LFS
	t.Cleanup(func() {
		Server, Database, Repository = savedServer, savedDatabase, savedRepository
		Security, Email, Session = savedSecurity, savedEmail, savedSession
		Cache, LFS = savedCache, savedLFS
	})
}

// seedValidGlobals fills the global configuration structs with a valid
// baseline so tests can break one setting at a time.
func seedValidGlobals() {
	Server.Protocol = "http"
	Server.HTTPPort = 3000
	Server.ExternalURL = "http://localhost:3000/"
	Server.URL = nil
	Server.GracefulShutdownTimeout = 30 * time.Second
	Database.Type = "sqlite3"
	Database.Path = "/tmp/gogs.db"
	Repository.Root = "/tmp/repos"
	Repository.Upload.Enabled = false
	Security.SecretKey = "a-strong-key"
	Email.Enabled = false
	Session.Provider = "memory"
	Cache.Adapter = "memory"
	LFS.Storage = "local"
	LFS.ObjectsPath = "/tmp/lfs"
	LFS.ObjectsTempPath = "/tmp/lfs/tmp"
}

func TestValidate_validBaseline(t *testing.T) {
	swapGlobals(t)
	seedValidGlobals()
	assert.Empty(t, validate())
}

func TestValidate_aggregatesProblems(t *testing.T) {
	swapGlobals(t)
	seedValidGlobals()

	Server.Protocol = "carrier-pigeon"
	Database.Type = "oracle"
	Security.SecretKey = ""
	Email.Enabled = true
	Email.Host = ""
	Email.From = "not-an-address"

	errs := validate()
	require.Len(t, errs, 5)

	joined := ValidationErrors(errs).Error()
	assert.Contains(t, joined, "[server] PROTOCOL")
	assert.Contains(t, joined, "[database] TYPE")
	assert.Contains(t, joined, "[security] SECRET_KEY")
	assert.Contains(t, joined, "[email] HOST")
	assert.Contains(t, joined, "[email] FROM")
	assert.Contains(t, joined, "5 problem(s)")
}

func TestValidate_neverEchoesSensitiveValues(t *testing.T) {
	swapGlobals(t)
	seedValidGlobals()

	Database.Type = "oracle"
	Database.Password = "db-password-leak"
	Security.SecretKey = "CHANGE-ME-OR-FAIL-TO-START"
	Email.Enabled = true
	Email.Host = ""
	Email.Password = "smtp-password-leak"

	joined := ValidationErrors(validate()).Error()
	assert.NotContains(t, joined, "db-password-leak")
	assert.NotContains(t, joined, "smtp-password-leak")
	assert.NotContains(t, joined, "CHANGE-ME-OR-FAIL-TO-START")
}

func TestValidate_emailFromFallsBackToUser(t *testing.T) {
	swapGlobals(t)
	seedValidGlobals()

	Email.Enabled = true
	Email.Host = "smtp.example.com:587"
	Email.From = ""
	Email.User = "gogs@example.com"

	assert.Empty(t, validate())
}
