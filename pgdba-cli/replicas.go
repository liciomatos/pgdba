package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/liciomatos/pgdba-cli/config"
)

// replicaSpec is one --replica name=URL (or --replica-vault name=vault-entry) flag.
type replicaSpec struct {
	Name string
	URL  string
}

// replicaFlags collects repeated --replica / --replica-vault flags. fromVault makes the
// value after "=" a vault entry name instead of a connection URI.
type replicaFlags struct {
	specs     *[]replicaSpec
	fromVault bool
}

func (f replicaFlags) String() string { return "" }

var replicaNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (f replicaFlags) Set(value string) error {
	name, target, found := strings.Cut(value, "=")
	if !found || name == "" || target == "" {
		expected := "name=postgres://..."
		if f.fromVault {
			expected = "name=vault-entry"
		}
		return fmt.Errorf("expected %s, got %q", expected, value)
	}
	if !replicaNamePattern.MatchString(name) {
		return fmt.Errorf("replica name %q may only contain letters, digits, '_' and '-'", name)
	}
	if name == "primary" {
		return fmt.Errorf(`"primary" is reserved for the main connection`)
	}
	for _, existing := range *f.specs {
		if existing.Name == name {
			return fmt.Errorf("replica %q given twice", name)
		}
	}
	if f.fromVault {
		uri, err := connectionURIFromVault(target)
		if err != nil {
			return fmt.Errorf("replica %s: vault: %w", name, err)
		}
		target = uri
	}
	*f.specs = append(*f.specs, replicaSpec{Name: name, URL: target})
	return nil
}

// majorVersion extracts the major version from SHOW server_version ("16.4", "17beta1").
func majorVersion(version string) int {
	digits := version
	if end := strings.IndexFunc(version, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
		digits = version[:end]
	}
	major, _ := strconv.Atoi(digits)
	return major
}

// connectReplicas opens and checks every replica. A replica must run the same major
// version as the primary: Fetch* functions pick version-specific columns from the
// primary's version, so a different major would produce SQL errors.
func connectReplicas(specs []replicaSpec, primaryVersion string) ([]*config.Target, error) {
	targets := make([]*config.Target, 0, len(specs))
	for _, spec := range specs {
		parsed, err := url.Parse(spec.URL)
		if err != nil {
			return nil, fmt.Errorf("replica %s: invalid URL: %w", spec.Name, err)
		}
		target := &config.Target{Name: spec.Name, Host: parsed.Hostname(), DBName: strings.TrimPrefix(parsed.Path, "/")}
		target.Port, _ = strconv.Atoi(parsed.Port())
		if target.DB, err = sql.Open("postgres", spec.URL); err != nil {
			return nil, fmt.Errorf("replica %s: %w", spec.Name, err)
		}
		if err := target.DB.Ping(); err != nil {
			return nil, fmt.Errorf("replica %s (%s): cannot reach server: %w", spec.Name, target.Host, err)
		}
		if err := target.DB.QueryRow("SHOW server_version;").Scan(&target.Version); err != nil {
			return nil, fmt.Errorf("replica %s: reading server version: %w", spec.Name, err)
		}
		if majorVersion(target.Version) != majorVersion(primaryVersion) {
			return nil, fmt.Errorf("replica %s runs PostgreSQL %s but the primary runs %s; targets must share the major version",
				spec.Name, target.Version, primaryVersion)
		}
		targets = append(targets, target)
	}
	return targets, nil
}
