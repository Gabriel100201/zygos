package main

import "testing"

// A release binary carries its version in the `version` variable, stamped by
// GoReleaser. Whatever is there wins — the build-info fallback must not
// second-guess it.
func TestResolvedVersionPrefersTheStampedValue(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "1.2.3"
	if got := resolvedVersion(); got != "1.2.3" {
		t.Errorf("resolvedVersion() = %q, want the stamped %q", got, "1.2.3")
	}
}

// `go install <module>@<version>` applies no ldflags, so version is still "dev"
// and the module version Go embedded in the binary has to fill in. Anything but
// the literal "dev" means the fallback found something; the test binary itself
// is built from a working tree, so the exact value depends on the checkout.
func TestResolvedVersionFallsBackToBuildInfo(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "dev"
	got := resolvedVersion()
	if got == "" {
		t.Fatal("resolvedVersion() returned empty")
	}
	if got[0] == 'v' {
		t.Errorf("resolvedVersion() = %q, want the leading v trimmed", got)
	}
}
