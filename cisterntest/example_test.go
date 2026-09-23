package cisterntest_test

import (
	"testing"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/cisterntest"
	"github.com/JonasBorgesLM/cistern/memory"
)

// A store implementation proves itself by running the suite from its own
// tests, returning a fresh, empty store for every subtest.
func ExampleRunStore() {
	_ = func(t *testing.T) {
		cisterntest.RunStore(t, func(t *testing.T) cistern.Store {
			s, err := memory.New()
			if err != nil {
				t.Fatal(err)
			}
			return s
		})
	}
}
