package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		name      string
		ldVersion string
		info      *debug.BuildInfo
		want      string
	}{
		{"ldflags override wins", "v1.2.3", buildInfo("v9.9.9"), "v1.2.3"},
		{"nil build info", "dev", nil, "dev"},
		{"tagged module version (go install @vX.Y.Z)", "dev", buildInfo("v0.1.0"), "v0.1.0"},
		{"(devel) is hidden", "dev", buildInfo("(devel)"), "dev"},
		{"empty is hidden", "dev", buildInfo(""), "dev"},
		// The three pseudo-version forms from the Go modules reference. These are
		// the strings `go build` embeds for an untagged commit since Go 1.24.
		{"pseudo version, no tag yet", "dev", buildInfo("v0.0.0-20260928154706-9667b859f76d"), "dev"},
		{"pseudo version, commit after a tag", "dev", buildInfo("v0.1.1-0.20260928154707-bf85eeda22a3"), "dev"},
		{"pseudo version, after a prerelease tag", "dev", buildInfo("v0.2.0-rc.1.0.20260928154707-bf85eeda22a3"), "dev"},
		{"dirty pseudo version", "dev", buildInfo("v0.1.1-0.20260928154707-bf85eeda22a3+dirty"), "dev"},
		{"dirty tag", "dev", buildInfo("v0.1.0+dirty"), "dev"},
		{"prerelease tag is a real release", "dev", buildInfo("v0.2.0-rc.1"), "v0.2.0-rc.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.ldVersion, tt.info); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func buildInfo(version string) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Version: version}}
}

func TestRun_Version(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Errorf("%s: exit code = %d, want 0", arg, code)
		}
		if !strings.HasPrefix(stdout.String(), "zip2win ") {
			t.Errorf("%s: stdout = %q, want \"zip2win <version>\"", arg, stdout.String())
		}
	}
}
