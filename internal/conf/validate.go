package conf

import (
	"fmt"
	"net/mail"
	"strings"

	"github.com/cockroachdb/errors"
)

// ValidationErrors aggregates every configuration problem found during
// initialization so operators can fix them in one pass instead of
// discovering them one startup failure at a time.
type ValidationErrors []error

// Error formats the problems as a numbered list, one per line.
func (e ValidationErrors) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "configuration is invalid (%d problem(s)):", len(e))
	for _, err := range e {
		fmt.Fprintf(&sb, "\n  - %s", err)
	}
	return sb.String()
}

// Unwrap returns the underlying errors for errors.Is and errors.As.
func (e ValidationErrors) Unwrap() []error {
	return []error(e)
}

// validate checks the mapped configuration for semantic problems. Messages
// must never include sensitive values such as passwords or secret keys.
func validate() []error {
	var errs []error

	// ***************************
	// ----- Server settings -----
	// ***************************

	switch Server.Protocol {
	case "http", "https", "fcgi", "unix":
	default:
		errs = append(errs, errors.Errorf(
			"[server] PROTOCOL: unsupported value %q, must be one of \"http\", \"https\", \"fcgi\", \"unix\"",
			Server.Protocol,
		))
	}
	if Server.Protocol == "http" || Server.Protocol == "https" {
		if Server.HTTPPort < 1 || Server.HTTPPort > 65535 {
			errs = append(errs, errors.Errorf("[server] HTTP_PORT: invalid port number %d", Server.HTTPPort))
		}
	}
	// A nil URL means the parse failure was already reported by Init.
	if Server.URL != nil {
		if Server.URL.Scheme != "http" && Server.URL.Scheme != "https" {
			errs = append(errs, errors.Errorf(
				"[server] EXTERNAL_URL: scheme must be \"http\" or \"https\", got %q",
				Server.ExternalURL,
			))
		} else if Server.URL.Host == "" {
			errs = append(errs, errors.Errorf("[server] EXTERNAL_URL: missing host in %q", Server.ExternalURL))
		}
	}
	if Server.GracefulShutdownTimeout <= 0 {
		errs = append(errs, errors.Errorf(
			"[server] GRACEFUL_SHUTDOWN_TIMEOUT: must be positive, got %s",
			Server.GracefulShutdownTimeout,
		))
	}

	// *****************************
	// ----- Database settings -----
	// *****************************

	switch Database.Type {
	case "postgres", "mysql":
		if Database.Host == "" {
			errs = append(errs, errors.New("[database] HOST: must not be empty"))
		}
		if Database.Name == "" {
			errs = append(errs, errors.New("[database] NAME: must not be empty"))
		}
		if Database.User == "" {
			errs = append(errs, errors.New("[database] USER: must not be empty"))
		}
	case "sqlite3":
		if Database.Path == "" {
			errs = append(errs, errors.New("[database] PATH: must not be empty for sqlite3"))
		}
	default:
		errs = append(errs, errors.Errorf(
			"[database] TYPE: unsupported value %q, must be one of \"postgres\", \"mysql\", \"sqlite3\"",
			Database.Type,
		))
	}

	// *******************************
	// ----- Repository settings -----
	// *******************************

	if Repository.Root == "" {
		errs = append(errs, errors.New("[repository] ROOT: must not be empty"))
	}
	if Repository.Upload.Enabled && Repository.Upload.TempPath == "" {
		errs = append(errs, errors.New("[repository.upload] TEMP_PATH: must not be empty when uploads are enabled"))
	}

	// *****************************
	// ----- Security settings -----
	// *****************************

	if Security.SecretKey == "" || Security.SecretKey == "CHANGE-ME-OR-FAIL-TO-START" {
		errs = append(errs, errors.New("[security] SECRET_KEY: must be set to a strong, unguessable value"))
	}

	// **************************
	// ----- Email settings -----
	// **************************

	if Email.Enabled {
		if Email.Host == "" {
			errs = append(errs, errors.New("[email] HOST: must not be empty when the email service is enabled"))
		}
		from := Email.From
		if from == "" {
			from = Email.User
		}
		if from == "" {
			errs = append(errs, errors.New("[email] FROM: must not be empty when the email service is enabled"))
		} else if _, err := mail.ParseAddress(from); err != nil {
			errs = append(errs, errors.Wrapf(err, "[email] FROM: invalid address %q", from))
		}
	}

	// ****************************
	// ----- Session settings -----
	// ****************************

	switch Session.Provider {
	case "memory", "file", "redis":
	default:
		errs = append(errs, errors.Errorf(
			"[session] PROVIDER: unsupported value %q, must be one of \"memory\", \"file\", \"redis\"",
			Session.Provider,
		))
	}

	// **************************
	// ----- Cache settings -----
	// **************************

	switch Cache.Adapter {
	case "memory", "redis":
	default:
		errs = append(errs, errors.Errorf(
			"[cache] ADAPTER: unsupported value %q, must be one of \"memory\", \"redis\"",
			Cache.Adapter,
		))
	}

	// *************************
	// ----- LFS settings -----
	// *************************

	if LFS.Storage == "local" {
		if LFS.ObjectsPath == "" {
			errs = append(errs, errors.New("[lfs] OBJECTS_PATH: must not be empty when local storage is used"))
		}
		if LFS.ObjectsTempPath == "" {
			errs = append(errs, errors.New("[lfs] OBJECTS_TEMP_PATH: must not be empty when local storage is used"))
		}
	}

	return errs
}
