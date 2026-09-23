package redisstore_test

import (
	"os"
	"strings"
	"testing"
)

// RS-09: the README documents the ACL the integration suite proves, word for
// word. A README that drifted from the tested user would send operators a
// user that either fails or grants more than it should.
func TestREADMEDocumentsTheTestedACL(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), minimalACL) {
		t.Fatalf("README.md does not contain the tested ACL:\n%s", minimalACL)
	}
}
