package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gogs.io/gogs/internal/context"
)

func TestRenderIndex_injection(t *testing.T) {
	customDir := t.TempDir()
	injectDir := filepath.Join(customDir, "templates", "inject")
	require.NoError(t, os.MkdirAll(injectDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(injectDir, "head.tmpl"), []byte(`<meta name="head-marker">`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(injectDir, "footer.tmpl"), []byte(`<script>footerMarker()</script>`), 0600))
	t.Setenv("GOGS_CUSTOM", customDir)

	inject, err := readInjectContent()
	require.NoError(t, err)

	shell := `<html><head>{{.WebContext}}</head><body><div id="root"></div></body></html>`
	got, err := renderIndex([]byte(shell), context.WebContext{Lang: "en-US"}, inject)
	require.NoError(t, err)

	out := string(got)
	assert.Contains(t, out, `<meta name="head-marker">`)
	assert.Contains(t, out, `<script>footerMarker()</script></body>`)
	// The head injection lands inside <head>, before </head>.
	assert.Less(t, strings.Index(out, `<meta name="head-marker">`), strings.Index(out, `</head>`))
}

func TestRenderIndex_subpath(t *testing.T) {
	shell := `<html><head>{{.WebContext}}</head><body>` +
		`<script src="./assets/index.js"></script>` +
		`<link href="./assets/index.css">` +
		`<script src="/src/main.tsx"></script>` +
		`<link href="/img/favicon.png">` +
		`</body></html>`

	got, err := renderIndex([]byte(shell), context.WebContext{Lang: "en-US", SubURL: "/gogs"}, injectContent{})
	require.NoError(t, err)

	out := string(got)
	// Asset and dev-server entrypoint paths are prefixed with the subpath so
	// the shell works identically behind a reverse proxy mount.
	assert.Contains(t, out, `src="/gogs/assets/index.js"`)
	assert.Contains(t, out, `href="/gogs/assets/index.css"`)
	assert.Contains(t, out, `src="/gogs/src/main.tsx"`)
	assert.Contains(t, out, `href="/gogs/img/favicon.png"`)

	// Without a subpath the shell stays untouched.
	got, err = renderIndex([]byte(shell), context.WebContext{Lang: "en-US"}, injectContent{})
	require.NoError(t, err)
	out = string(got)
	assert.Contains(t, out, `src="./assets/index.js"`)
	assert.Contains(t, out, `src="/src/main.tsx"`)
}
