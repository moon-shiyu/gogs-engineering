package web

import (
	stdctx "context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/cockroachdb/errors"
	log "unknwon.dev/clog/v2"

	"gogs.io/gogs/internal/bootid"
	"gogs.io/gogs/internal/conf"
	"gogs.io/gogs/internal/osx"
)

// serveGracefully starts the configured HTTP, HTTPS, or Unix socket server
// and blocks until it stops. On SIGINT or SIGTERM, in-flight requests are
// given up to [server] GRACEFUL_SHUTDOWN_TIMEOUT to finish before the server
// is closed, so confirmed work is never truncated by a shutdown.
func serveGracefully(handler http.Handler, listenAddr string) error {
	sigCtx, stop := signal.NotifyContext(stdctx.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, listenAndServe, err := newServer(handler, listenAddr)
	if err != nil {
		return err
	}
	return runServer(server, listenAndServe, sigCtx.Done())
}

// newServer builds the HTTP server and its listen function for the
// configured protocol.
func newServer(handler http.Handler, listenAddr string) (*http.Server, func() error, error) {
	server := &http.Server{
		Addr:    listenAddr,
		Handler: handler,
	}

	switch conf.Server.Protocol {
	case "http":
		return server, server.ListenAndServe, nil

	case "https":
		tlsMinVersion := tls.VersionTLS12
		switch conf.Server.TLSMinVersion {
		case "TLS13":
			tlsMinVersion = tls.VersionTLS13
		case "TLS12":
			tlsMinVersion = tls.VersionTLS12
		case "TLS11":
			tlsMinVersion = tls.VersionTLS11
		case "TLS10":
			tlsMinVersion = tls.VersionTLS10
		}
		server.TLSConfig = &tls.Config{
			MinVersion:               uint16(tlsMinVersion),
			CurvePreferences:         []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521},
			PreferServerCipherSuites: true,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			},
		}
		return server, func() error {
			return server.ListenAndServeTLS(conf.Server.CertFile, conf.Server.KeyFile)
		}, nil

	case "unix":
		if osx.Exist(listenAddr) {
			if err := os.Remove(listenAddr); err != nil {
				return nil, nil, errors.Wrap(err, "remove existing Unix domain socket")
			}
		}

		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: listenAddr, Net: "unix"})
		if err != nil {
			return nil, nil, errors.Wrap(err, "listen on Unix network")
		}
		if err = os.Chmod(listenAddr, conf.Server.UnixSocketMode); err != nil {
			_ = listener.Close()
			_ = os.Remove(listenAddr)
			return nil, nil, errors.Wrap(err, "change permission of Unix domain socket")
		}
		return server, func() error { return server.Serve(listener) }, nil
	}
	return nil, nil, errors.Newf("unexpected server protocol: %s", conf.Server.Protocol)
}

// runServer runs the server until it fails or stop closes. When stop closes,
// in-flight requests are given up to the configured graceful shutdown
// timeout to finish before the server is closed.
func runServer(server *http.Server, listenAndServe func() error, stop <-chan struct{}) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- listenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.Wrap(err, "start server")

	case <-stop:
	}

	log.Info("Received shutdown signal, waiting up to %s for in-flight requests (instance %s)", conf.Server.GracefulShutdownTimeout, bootid.Instance())
	shutdownCtx, cancel := stdctx.WithTimeout(stdctx.Background(), conf.Server.GracefulShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("Failed to shut down gracefully, closing the server now: %v", err)
		_ = server.Close()
	}
	if conf.Server.Protocol == "unix" {
		_ = os.Remove(server.Addr)
	}
	log.Info("Server stopped (instance %s)", bootid.Instance())
	return nil
}
