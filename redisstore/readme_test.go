package redisstore_test

import (
	"os"
	"strings"
	"testing"
)

// RS-09: the README documents the ACL the integration suite proves, word for
// word. A README that drifted from the tested user would send operators a
// user that either fails or grants more than it should.
// Negative control: verified failing with +keys appended to the README line.
func TestREADMEDocumentsTheTestedACL(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, line := range strings.Split(string(readme), "\n") {
		if strings.HasPrefix(line, "ACL SETUSER") {
			documented = append(documented, line)
		}
	}
	// The whole line, not a substring: permissions appended after the tested
	// ones would still contain it, and that line is what operators copy (#120).
	if len(documented) != 1 || documented[0] != minimalACL {
		t.Fatalf("README.md documents %q, want exactly one ACL line:\n%s", documented, minimalACL)
	}
}
