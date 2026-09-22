// Package-level documentation: layered configuration sources.
//
// Values are resolved in a fixed order, later sources win:
//
//  1. Embedded defaults (conf/app.ini compiled into the binary).
//  2. Custom configuration file (custom/conf/app.ini or --config).
//  3. Environment variables prefixed with GOGS__.
//  4. Command line overrides registered via CommandLineOverrides.
package conf

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/ini.v1"
	log "unknwon.dev/clog/v2"
)

// ValueSource identifies where a configuration value came from.
type ValueSource string

const (
	SourceDefault     ValueSource = "default"
	SourceFile        ValueSource = "config file"
	SourceEnvironment ValueSource = "environment variable"
	SourceCommandLine ValueSource = "command line"
)

// EnvOverridePrefix is the prefix of environment variables that override
// configuration values. The remainder of the variable name is the section
// name (uppercased, with dots replaced by underscores) and the key name,
// joined by a double underscore, e.g. GOGS__DATABASE__HOST overrides
// [database] HOST and GOGS__REPOSITORY_UPLOAD__TEMP_PATH overrides
// [repository.upload] TEMP_PATH.
//
// A variable that is set to an empty string overrides the value to empty,
// which is intentionally different from the variable being unset.
const EnvOverridePrefix = "GOGS__"

// Override is a single configuration value override.
type Override struct {
	Section string
	Key     string
	Value   string
}

// CommandLineOverrides are applied after all other sources when the next
// Init runs. Command entry points set this before calling Init.
var CommandLineOverrides []Override

// valueSources records the source of every known configuration value,
// keyed by section name then key name. It is rebuilt on every Init.
var valueSources = map[string]map[string]ValueSource{}

// markAllSources records the given source for every key currently loaded.
func markAllSources(f *ini.File, source ValueSource) {
	for _, sec := range f.Sections() {
		for _, k := range sec.Keys() {
			markSource(sec.Name(), k.Name(), source)
		}
	}
}

// markFileSources records the given source for every key defined in the
// parsed custom configuration file. A nil file is ignored.
func markFileSources(f *ini.File, source ValueSource) {
	if f == nil {
		return
	}
	markAllSources(f, source)
}

func markSource(section, key string, source ValueSource) {
	keys, ok := valueSources[section]
	if !ok {
		keys = map[string]ValueSource{}
		valueSources[section] = keys
	}
	keys[key] = source
}

// SourceOf returns the source of the given configuration value.
func SourceOf(section, key string) ValueSource {
	if keys, ok := valueSources[section]; ok {
		if source, ok := keys[key]; ok {
			return source
		}
	}
	return SourceDefault
}

// sectionEnvName converts a section name to its environment variable form,
// e.g. "repository.upload" becomes "REPOSITORY_UPLOAD".
func sectionEnvName(section string) string {
	return strings.ToUpper(strings.ReplaceAll(section, ".", "_"))
}

// applyEnvironmentOverrides applies GOGS__ environment variables to the
// loaded configuration. Variables naming an unknown section still create the
// section so the mistake is surfaced by checkInvalidOptions.
func applyEnvironmentOverrides(f *ini.File) {
	sectionByEnvName := make(map[string]string)
	envNames := make([]string, 0)
	for _, name := range f.SectionStrings() {
		envName := sectionEnvName(name)
		sectionByEnvName[envName] = name
		envNames = append(envNames, envName)
	}
	// Match the longest section name first so that e.g. REPOSITORY_UPLOAD
	// wins over REPOSITORY for GOGS__REPOSITORY_UPLOAD__TEMP_PATH.
	sort.Slice(envNames, func(i, j int) bool { return len(envNames[i]) > len(envNames[j]) })

	for _, env := range os.Environ() {
		name, value, ok := strings.Cut(env, "=")
		if !ok || !strings.HasPrefix(name, EnvOverridePrefix) {
			continue
		}

		rest := name[len(EnvOverridePrefix):]
		var section, key string
		for _, envName := range envNames {
			if strings.HasPrefix(rest, envName+"__") {
				section = sectionByEnvName[envName]
				key = rest[len(envName)+2:]
				break
			}
		}
		if section == "" {
			head, tail, found := strings.Cut(rest, "__")
			if !found || head == "" || tail == "" {
				continue
			}
			section = strings.ToLower(head)
			key = tail
		}

		sec := f.Section(section)
		// Reuse the canonical key casing when the key already exists, and
		// fall back to the conventional uppercase form otherwise.
		keyName := ""
		for _, existing := range sec.KeyStrings() {
			if strings.EqualFold(existing, key) {
				keyName = existing
				break
			}
		}
		if keyName == "" {
			keyName = strings.ToUpper(key)
		}
		sec.Key(keyName).SetValue(value)
		markSource(section, keyName, SourceEnvironment)
	}
}

// applyOverrides applies explicit overrides to the loaded configuration.
func applyOverrides(f *ini.File, overrides []Override, source ValueSource) {
	for _, o := range overrides {
		f.Section(o.Section).Key(o.Key).SetValue(o.Value)
		markSource(o.Section, o.Key, source)
	}
}

// parseShadowFile parses a configuration file on its own, keeping repeated
// keys as shadows. It returns nil when the file cannot be parsed, in which
// case the main load reports the authoritative error.
func parseShadowFile(path string) *ini.File {
	f, err := ini.LoadSources(ini.LoadOptions{
		AllowShadows:               true,
		AllowDuplicateShadowValues: true,
		IgnoreInlineComment:        true,
	}, path)
	if err != nil {
		return nil
	}
	return f
}

// duplicateKeyWarnings returns a warning for every key defined more than
// once in the custom configuration file. The main load keeps the last value,
// so the warnings state exactly which value wins.
func duplicateKeyWarnings(f *ini.File, path string) []string {
	var warnings []string
	for _, sec := range f.Sections() {
		for _, k := range sec.Keys() {
			if n := len(k.ValueWithShadows()); n > 1 {
				warnings = append(warnings, fmt.Sprintf("option [%s] %s is defined %d times in %s, using the last value", sec.Name(), k.Name(), n, path))
			}
		}
	}
	return warnings
}

// sensitiveKey reports whether the value of the given key must not appear in
// logs. Matching is by key name and never by value.
func sensitiveKey(section, key string) bool {
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") {
		return true
	}
	// Connection strings and webhook URLs embed credentials.
	switch section + "." + upper {
	case "cache.HOST",
		"session.PROVIDER_CONFIG",
		"log.slack.URL",
		"log.discord.URL":
		return true
	}
	return false
}

const redactedValue = "<redacted>"

// effectiveConfigLine is one entry of the effective configuration dump.
type effectiveConfigLine struct {
	Section string
	Key     string
	Value   string
	Source  ValueSource
}

// effectiveConfig returns the resolved configuration with sensitive values
// redacted, ordered by section and key.
func effectiveConfig(f *ini.File) []effectiveConfigLine {
	var lines []effectiveConfigLine
	for _, sec := range f.Sections() {
		for _, k := range sec.Keys() {
			value := k.String()
			if sensitiveKey(sec.Name(), k.Name()) {
				value = redactedValue
			}
			lines = append(lines, effectiveConfigLine{
				Section: sec.Name(),
				Key:     k.Name(),
				Value:   value,
				Source:  SourceOf(sec.Name(), k.Name()),
			})
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		if lines[i].Section != lines[j].Section {
			return lines[i].Section < lines[j].Section
		}
		return lines[i].Key < lines[j].Key
	})
	return lines
}

// LogEffectiveConfig logs the resolved configuration with sensitive values
// redacted. Values coming from beyond the embedded defaults are logged at
// Info level with their source, and the full dump is logged at Trace level.
func LogEffectiveConfig() {
	if File == nil {
		return
	}
	for _, l := range effectiveConfig(File) {
		log.Trace("Effective config: [%s] %s = %q (%s)", l.Section, l.Key, l.Value, l.Source)
		if l.Source != SourceDefault {
			log.Info("Config from %s: [%s] %s = %q", l.Source, l.Section, l.Key, l.Value)
		}
	}
}
