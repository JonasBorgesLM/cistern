package cistern_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
)

// RS-03, T-02: a key that could inject into a log line or collide ambiguously
// is refused on every operation, before any store is touched.
// Negative control: verified failing with the control-character check removed.
func TestInvalidKeysAreRefusedOnEveryOperation(t *testing.T) {
	for name, key := range map[string]string{
		"empty":             "",
		"NUL":               "user:\x00:list",
		"newline":           "user:42\nlevel=admin",
		"DEL":               "user:42\x7f",
		"C1 control (NEL)":  "user:42\u0085",
		"invalid UTF-8":     "user:\xff",
		"longer than 256 B": strings.Repeat("a", 257),
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			l2 := newRecorder()
			c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute))
			var calls atomic.Int32

			if _, _, err := c.Get(ctx, key); !errors.Is(err, cistern.ErrInvalidKey) {
				t.Errorf("Get: err = %v, want ErrInvalidKey", err)
			}
			if _, err := c.GetOrLoad(ctx, key, countingLoader("v", &calls)); !errors.Is(err, cistern.ErrInvalidKey) {
				t.Errorf("GetOrLoad: err = %v, want ErrInvalidKey", err)
			}
			if err := c.Set(ctx, key, "v"); !errors.Is(err, cistern.ErrInvalidKey) {
				t.Errorf("Set: err = %v, want ErrInvalidKey", err)
			}
			if err := c.Delete(ctx, key); !errors.Is(err, cistern.ErrInvalidKey) {
				t.Errorf("Delete: err = %v, want ErrInvalidKey", err)
			}
			if calls.Load() != 0 || l2.gets != 0 || len(l2.keys()) != 0 {
				t.Error("an invalid key reached the loader or the store")
			}
		})
	}
}

func TestValidKeysAreAccepted(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{"k", "user:42:list:7", "tarefa:ação", strings.Repeat("a", 256)} {
		c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute))
		if err := c.Set(ctx, key, "v"); err != nil {
			t.Fatalf("Set(%q): %v", key, err)
		}
		if v, ok, err := c.Get(ctx, key); err != nil || !ok || v != "v" {
			t.Fatalf("Get(%q) = %q, %v, %v", key, v, ok, err)
		}
	}
}

// ADR-0008: with WithKeyHashing a key over 256 bytes is stored as h:<sha256>;
// keys within the limit are never hashed.
// Negative control: verified failing with long keys stored verbatim.
func TestKeyHashing(t *testing.T) {
	ctx := context.Background()
	long := "user:42:search:" + strings.Repeat("q", 300)
	sum := sha256.Sum256([]byte(long))

	l2 := newRecorder()
	c := newCache(t, cistern.WithL2(l2), cistern.WithTTL(time.Minute), cistern.WithKeyHashing())
	if err := c.Set(ctx, long, "results"); err != nil {
		t.Fatalf("Set of a long key with hashing: %v", err)
	}
	if err := c.Set(ctx, "user:42:list", "short"); err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]bool{
		"cistern:v1:tasks:-:h:" + hex.EncodeToString(sum[:]): true,
		"cistern:v1:tasks:-:k:user:42:list":                  true,
	}
	for _, k := range l2.keys() {
		if !wantKeys[k] {
			t.Fatalf("stored key %q, want one of %v", k, wantKeys)
		}
		delete(wantKeys, k)
	}
	if len(wantKeys) != 0 {
		t.Fatalf("missing stored keys %v", wantKeys)
	}

	if v, ok, err := c.Get(ctx, long); err != nil || !ok || v != "results" {
		t.Fatalf("Get of the hashed key = %q, %v, %v", v, ok, err)
	}
	other := "user:42:search:" + strings.Repeat("r", 300)
	if _, ok, _ := c.Get(ctx, other); ok {
		t.Fatal("two different long keys resolved to the same entry")
	}
	if err := c.Delete(ctx, long); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(ctx, long); ok {
		t.Fatal("Delete did not remove the hashed entry")
	}
}

// Hashing lifts only the length limit; a long key is still checked for
// control characters and UTF-8 (ADR-0008).
func TestKeyHashingDoesNotBypassTheOtherChecks(t *testing.T) {
	c := newCache(t, cistern.WithL1(newL1(t)), cistern.WithTTL(time.Minute), cistern.WithKeyHashing())
	long := strings.Repeat("a", 300) + "\n"
	if err := c.Set(context.Background(), long, "v"); !errors.Is(err, cistern.ErrInvalidKey) {
		t.Fatalf("Set: err = %v, want ErrInvalidKey", err)
	}
}
