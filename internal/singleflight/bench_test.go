package singleflight_test

import (
	"context"
	"testing"

	"github.com/JonasBorgesLM/cistern/internal/singleflight"
)

// RNF-06: the overhead of coalescing, with every goroutine on one key.
func BenchmarkDoContended(b *testing.B) {
	var g singleflight.Group[int]
	ctx := context.Background()
	fn := func(context.Context) (int, error) { return 1, nil }
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := g.Do(ctx, "k", long, fn); err != nil {
				b.Error(err)
			}
		}
	})
}
