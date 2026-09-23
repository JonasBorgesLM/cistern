package cistern_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/JonasBorgesLM/cistern"
)

func TestSentinelErrorsAreDistinctAndSurviveWrapping(t *testing.T) {
	sentinels := map[string]error{
		"ErrNotFound":      cistern.ErrNotFound,
		"ErrInvalidConfig": cistern.ErrInvalidConfig,
	}
	for name, err := range sentinels {
		t.Run(name, func(t *testing.T) {
			if err == nil {
				t.Fatal("sentinel is nil")
			}
			wrapped := fmt.Errorf("loading task 7: %w", err)
			if !errors.Is(wrapped, err) {
				t.Fatalf("errors.Is(wrapped, %s) = false", name)
			}
			for other, otherErr := range sentinels {
				if other != name && errors.Is(wrapped, otherErr) {
					t.Fatalf("%s is indistinguishable from %s", name, other)
				}
			}
		})
	}
}
