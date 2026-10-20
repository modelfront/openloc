package auth

// Every org-creation path has to provision the SAME set of defaults. There are
// three of them (signup, platform console, dev/OAuth), they were written at
// different times, and nothing links them — so a fourth added later will look
// complete while quietly producing an org that behaves differently from a real
// one.
//
// This pins the invariant at the only place it is visible: the source. It is a
// lint, not a behaviour test, because the behaviour it guards is an ABSENCE —
// a call that was never written.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The marker for "this function provisions a new tenant". mtdefaults has been
// on every path since the beginning, so it is the reliable anchor.
// The call form, not the bare name: workflowdefault.go names it in a comment
// describing the provisioning set, which is documentation rather than a path.
const provisioningMarker = "mtdefaults.EnsureForTenant("

// What every provisioning path must also do.
var mustAlsoProvision = []string{"dashboarddefaults.InstallDefaults("}

func TestEveryProvisioningPathInstallsEveryDefault(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var checked int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(body)
		if !strings.Contains(src, provisioningMarker) {
			continue
		}
		checked++
		for _, want := range mustAlsoProvision {
			if !strings.Contains(src, want) {
				t.Errorf("%s provisions a tenant (%s) but never calls %s; "+
					"that org will start with no dashboards while every other one has them",
					name, provisioningMarker, want)
			}
		}
	}
	// If the marker ever moves, this test would pass by examining nothing.
	if checked == 0 {
		t.Fatalf("found no provisioning path containing %q — the marker is stale and this test is inert", provisioningMarker)
	}
}
