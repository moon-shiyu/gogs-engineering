// Package bootid provides process-scoped and request-scoped identifiers used
// to correlate log lines emitted by a single running instance.
package bootid

import (
	"crypto/rand"
	"encoding/hex"
)

var instanceID = generate()

func generate() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf)
}

// Instance returns the identifier of the current process instance. It is
// stable for the lifetime of the process.
func Instance() string {
	return instanceID
}

// New returns a fresh unique identifier, e.g. for correlating a single
// request across subsystems.
func New() string {
	return generate()
}
