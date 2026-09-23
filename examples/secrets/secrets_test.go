package secrets_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JonasBorgesLM/moat/secret"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/examples/secrets"
	"github.com/JonasBorgesLM/cistern/memory"
)

// RS-04: a type marked NoCache cannot be cached at all.
func TestMarkedSecretsCannotBeCached(t *testing.T) {
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	_, err = cistern.New[string, secrets.Credential]("creds", func(k string) string { return k }, cistern.WithL1(l1), cistern.WithTTL(time.Minute))
	if !errors.Is(err, cistern.ErrUncacheable) {
		t.Fatalf("New = %v, want ErrUncacheable", err)
	}
}

// Why the mark matters: without it, a secret.Value is not leaked into the
// store — moat redacts it — but the entry can never be read back: every read
// misses, and only a decode error in the hooks says why.
func TestUnmarkedSecretsNeverLeakAndNeverHit(t *testing.T) {
	type unmarked struct {
		Token secret.Value `json:"token"`
	}
	plaintext := "never-in-the-store-" + t.Name() // built at run time: no credential-shaped literal
	ctx := context.Background()
	l1, err := memory.New()
	if err != nil {
		t.Fatal(err)
	}
	var decodeErrors int
	c, err := cistern.New[string, unmarked]("creds", func(k string) string { return k }, cistern.WithL1(l1), cistern.WithTTL(time.Minute),
		cistern.WithHooks(cistern.Hooks{OnError: func(_ context.Context, e cistern.ErrorEvent) {
			if e.Op == cistern.OpDecode {
				decodeErrors++
			}
		}}))
	if err != nil {
		t.Fatal(err)
	}
	original := unmarked{Token: secret.New([]byte(plaintext))}
	if err = c.Set(ctx, "billing", original); err != nil {
		t.Fatal(err)
	}

	stored, ok, err := l1.Get(ctx, "cistern:v1:creds:-:k:billing")
	if err != nil || !ok {
		t.Fatalf("raw read = %v, %v", ok, err)
	}
	if strings.Contains(string(stored), plaintext) {
		t.Fatal("the secret reached the store in clear")
	}
	if !strings.Contains(string(stored), secret.Redacted) {
		t.Fatalf("stored entry %q does not hold the redaction marker", stored)
	}
	if _, ok, err := c.Get(ctx, "billing"); ok || err != nil {
		t.Fatalf("Get = %v, %v; want a miss: a redacted secret cannot be decoded back", ok, err)
	}
	if decodeErrors != 1 {
		t.Fatalf("decode errors reported = %d, want 1", decodeErrors)
	}
}
