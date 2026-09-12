package factory

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	fullVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.\-]+)?$`)
	bareMinor   = regexp.MustCompile(`^v\d+\.\d+$`)
)

// IsFullVersion reports whether v is a complete Talos version such as v1.14.2 or v1.14.2-beta.0.
func IsFullVersion(v string) bool {
	return fullVersion.MatchString(v)
}

// IsBareMinor reports whether v names a minor such as v1.14.
func IsBareMinor(v string) bool {
	return bareMinor.MatchString(v)
}

// ResolveVersion returns a full version for requested: a full version is
// returned as is, a bare minor resolves to its highest released patch among
// versions (prereleases excluded). Anything else is an error.
func ResolveVersion(requested string, versions []string) (string, error) {
	requested = strings.TrimSpace(requested)

	if IsFullVersion(requested) {
		return requested, nil
	}

	if !IsBareMinor(requested) {
		return "", fmt.Errorf("%q is neither a full Talos version (v1.14.2) nor a bare minor (v1.14)", requested)
	}

	prefix := requested + "."
	best, bestPatch := "", -1

	for _, v := range versions {
		if !strings.HasPrefix(v, prefix) || strings.Contains(v, "-") {
			continue
		}

		patch, err := strconv.Atoi(strings.TrimPrefix(v, prefix))
		if err != nil {
			continue
		}

		if patch > bestPatch {
			best, bestPatch = v, patch
		}
	}

	if best == "" {
		return "", fmt.Errorf("no released patch of %s among the %d versions the factory serves", requested, len(versions))
	}

	return best, nil
}
