package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/ini.v1"
)

func loadTestIni(t *testing.T, data string) *ini.File {
	t.Helper()
	f, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, []byte(data))
	require.NoError(t, err)
	return f
}

func TestApplyEnvironmentOverrides(t *testing.T) {
	f := loadTestIni(t, `
[server]
HTTP_PORT = 3000
HTTP_ADDR = 0.0.0.0

[repository.upload]
TEMP_PATH = /tmp/uploads
`)

	t.Setenv("GOGS__SERVER__HTTP_PORT", "4000")
	// An explicitly empty value overrides to empty, which is different from
	// the variable being unset.
	t.Setenv("GOGS__SERVER__HTTP_ADDR", "")
	t.Setenv("GOGS__REPOSITORY_UPLOAD__TEMP_PATH", "/var/tmp/uploads")

	applyEnvironmentOverrides(f)

	assert.Equal(t, "4000", f.Section("server").Key("HTTP_PORT").String())
	assert.Equal(t, "", f.Section("server").Key("HTTP_ADDR").String())
	assert.Equal(t, "/var/tmp/uploads", f.Section("repository.upload").Key("TEMP_PATH").String())
}

func TestApplyEnvironmentOverrides_unsetKeepsValue(t *testing.T) {
	f := loadTestIni(t, `
[server]
HTTP_PORT = 3000
`)
	applyEnvironmentOverrides(f)
	assert.Equal(t, "3000", f.Section("server").Key("HTTP_PORT").String())
}

func TestApplyEnvironmentOverrides_unknownSectionStillCreated(t *testing.T) {
	f := loadTestIni(t, `
[server]
HTTP_PORT = 3000
`)
	t.Setenv("GOGS__BRANDNEW__THING", "x")
	applyEnvironmentOverrides(f)
	assert.Equal(t, "x", f.Section("brandnew").Key("THING").String())
}

func TestApplyEnvironmentOverrides_marksSource(t *testing.T) {
	ResetValueSourcesForTest(t)

	f := loadTestIni(t, `
[server]
HTTP_PORT = 3000
`)
	markAllSources(f, SourceDefault)
	t.Setenv("GOGS__SERVER__HTTP_PORT", "4000")
	applyEnvironmentOverrides(f)

	assert.Equal(t, SourceEnvironment, SourceOf("server", "HTTP_PORT"))
	assert.Equal(t, SourceDefault, SourceOf("server", "HTTP_ADDR"))
}

func TestApplyOverrides_commandLineWins(t *testing.T) {
	ResetValueSourcesForTest(t)

	f := loadTestIni(t, `
[server]
HTTP_PORT = 3000
`)
	markAllSources(f, SourceDefault)
	t.Setenv("GOGS__SERVER__HTTP_PORT", "4000")
	applyEnvironmentOverrides(f)
	applyOverrides(f, []Override{{Section: "server", Key: "HTTP_PORT", Value: "5000"}}, SourceCommandLine)

	assert.Equal(t, "5000", f.Section("server").Key("HTTP_PORT").String())
	assert.Equal(t, SourceCommandLine, SourceOf("server", "HTTP_PORT"))
}

func TestDuplicateKeyWarnings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.ini")
	require.NoError(t, os.WriteFile(path, []byte(`
[server]
HTTP_PORT = 3000
HTTP_PORT = 4000
`), 0o600))

	f := parseShadowFile(path)
	require.NotNil(t, f)

	warnings := duplicateKeyWarnings(f, path)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "[server] HTTP_PORT")
	assert.Contains(t, warnings[0], "2 times")
	assert.Contains(t, warnings[0], "using the last value")
}

func TestEffectiveConfig_redactsSensitiveValues(t *testing.T) {
	ResetValueSourcesForTest(t)

	f := loadTestIni(t, `
BRAND_NAME = Gogs

[security]
SECRET_KEY = super-secret-key

[database]
PASSWORD = hunter2
HOST = db.internal
`)
	for _, line := range effectiveConfig(f) {
		switch line.Key {
		case "SECRET_KEY", "PASSWORD":
			assert.Equal(t, redactedValue, line.Value, "%s must be redacted", line.Key)
		case "HOST":
			assert.Equal(t, "db.internal", line.Value)
		case "BRAND_NAME":
			assert.Equal(t, "Gogs", line.Value)
		}
	}
}

// ResetValueSourcesForTest clears the source registry and restores it when
// the test finishes.
func ResetValueSourcesForTest(t *testing.T) {
	t.Helper()
	saved := valueSources
	valueSources = map[string]map[string]ValueSource{}
	t.Cleanup(func() { valueSources = saved })
}
