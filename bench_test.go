package cistern_test

import (
	"context"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/memory"
)

// A representative list payload: the L1 hit cost is dominated by decoding it
// (ADR-0014), so the value's shape matters more than the store.
type benchList struct {
	ID    int      `json:"id"`
	Owner int      `json:"owner"`
	Title string   `json:"title"`
	Tasks []string `json:"tasks"`
}

// RNF-06: the L1 hit path, including the decode ADR-0014 accepts.
func BenchmarkGetL1Hit(b *testing.B) {
	l1, err := memory.New()
	if err != nil {
		b.Fatal(err)
	}
	c, err := cistern.New[string, benchList]("tasks", func(k string) string { return k },
		cistern.WithL1(l1), cistern.WithTTL(time.Hour))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	v := benchList{ID: 1, Owner: 42, Title: "today", Tasks: []string{"a", "b", "c", "d", "e", "f", "g", "h"}}
	if err := c.Set(ctx, "user:42:list:1", v); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, ok, _ := c.Get(ctx, "user:42:list:1"); !ok {
			b.Fatal("miss")
		}
	}
}
