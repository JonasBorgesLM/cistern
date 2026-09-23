package memory

import (
	"context"
	"testing"
	"time"
)

// An internal test, so the clock can be replaced without exporting a hook:
// gosec loads an external test package without export_test.go files.
func TestEntryExpiresAfterTTL(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return now }
	ctx := context.Background()

	if err := s.Set(ctx, "a", []byte("1"), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(9 * time.Second)
	if v, ok, err := s.Get(ctx, "a"); err != nil || !ok || string(v) != "1" {
		t.Fatalf("Get just before expiry = %q, %v, %v; want \"1\", true, nil", v, ok, err)
	}
	now = now.Add(time.Second)
	if v, ok, err := s.Get(ctx, "a"); err != nil || ok {
		t.Fatalf("Get at expiry = %q, %v, %v; want a miss", v, ok, err)
	}
}
