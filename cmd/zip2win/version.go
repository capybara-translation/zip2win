package main

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// version is set at release time with -ldflags "-X main.version=vX.Y.Z"
// (GoReleaser does this). Without that, currentVersion falls back to the module
// version Go embeds in the binary, which is the tag for `go install ...@vX.Y.Z`.
var version = "dev"

// pseudoVersionSuffix matches the timestamp-and-commit tail shared by every Go
// pseudo-version form:
//
//	vX.0.0-yyyymmddhhmmss-abcdefabcdef          (no tag yet)
//	vX.Y.(Z+1)-0.yyyymmddhhmmss-abcdefabcdef    (commits after vX.Y.Z)
//	vX.Y.Z-pre.0.yyyymmddhhmmss-abcdefabcdef    (commits after a prerelease)
var pseudoVersionSuffix = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)

// currentVersion returns the version to report for this binary.
func currentVersion() string {
	info, _ := debug.ReadBuildInfo()
	return resolveVersion(version, info)
}

// resolveVersion picks the version from the ldflags value or, failing that,
// from the build info. Since Go 1.24, a plain `go build` embeds a
// pseudo-version for an untagged commit and appends "+dirty" for uncommitted
// changes. Those are reported as "dev": printed as-is they look like releases
// and make bug reports ambiguous about which build is running.
func resolveVersion(ldVersion string, info *debug.BuildInfo) string {
	if ldVersion != "dev" {
		return ldVersion
	}
	if info == nil {
		return "dev"
	}
	v := info.Main.Version
	if v == "" || v == "(devel)" || strings.Contains(v, "+dirty") || pseudoVersionSuffix.MatchString(v) {
		return "dev"
	}
	return v
}
