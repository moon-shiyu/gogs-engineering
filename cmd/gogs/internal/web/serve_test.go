package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gogs.io/gogs/internal/conf"
)

func swapServerConf(t *testing.T, protocol string) {
	t.Helper()
	saved := conf.Server
	conf.Server.Protocol = protocol
	conf.Server.GracefulShutdownTimeout = 5 * time.Second
	t.Cleanup(func() { conf.Server = saved })
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestRunServer_gracefulShutdownWaitsForInFlight(t *testing.T) {
	swapServerConf(t, "http")

	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte("confirmed"))
	})

	port := freePort(t)
	server, listenAndServe, err := newServer(handler, fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runServer(server, listenAndServe, stop)
	}()

	// Wait for the server to accept connections.
	var conn net.Conn
	require.Eventually(t, func() bool {
		conn, err = net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	_ = conn.Close()

	// Start an in-flight request, then ask the server to stop.
	responseCh := make(chan string, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			responseCh <- "error: " + err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		responseCh <- string(body)
	}()

	// Give the request a moment to reach the handler, then stop the server.
	time.Sleep(100 * time.Millisecond)
	close(stop)

	// The in-flight request must be allowed to finish within the timeout.
	close(release)
	select {
	case got := <-responseCh:
		assert.Equal(t, "confirmed", got, "the confirmed response must not be truncated")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the in-flight response")
	}

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop within the graceful timeout")
	}
}

func TestRunServer_unixSocketRemovedOnShutdown(t *testing.T) {
	swapServerConf(t, "unix")
	conf.Server.UnixSocketMode = 0o666

	// Unix socket paths are length-limited (104 bytes on macOS), so the
	// socket must live in a short path rather than the test's temp dir.
	socketDir, err := os.MkdirTemp("/tmp", "gs")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "s.sock")
	server, listenAndServe, err := newServer(http.NewServeMux(), socketPath)
	require.NoError(t, err)

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runServer(server, listenAndServe, stop)
	}()

	require.Eventually(t, func() bool {
		_, err := os.Stat(socketPath)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)

	close(stop)
	require.NoError(t, <-done)
	assert.NoFileExists(t, socketPath, "the socket file must be removed on shutdown")
}
