// Package tedversion describes the release and native compatibility boundary.
package tedversion

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var base string

// Version is set by release builds; local builds derive it from version.txt.
var Version string

func Release() string {
	if Version != "" {
		return Version
	}
	return strings.TrimSpace(base) + ".0-dev"
}

// Major is deliberately derived from the committed contract, not API route v1.
func Major() string { return strings.Split(strings.TrimSpace(base), ".")[0] }

// Compatibility treats minor releases as breaking during the 0.x alpha.
func Compatibility() string {
	parts := strings.Split(strings.TrimSpace(base), ".")
	if parts[0] == "0" {
		return strings.Join(parts[:2], ".")
	}
	return parts[0]
}
