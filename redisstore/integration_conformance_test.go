//go:build integration

package redisstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/cisterntest"
)

// RF-14 and the C5 exit criterion: redisstore passes the same conformance
// suite as memory, against a real Redis. One container serves every subtest;
// FLUSHDB gives each the empty store the suite requires.
func TestConformance(t *testing.T) {
	r := startRedis(t)
	cisterntest.RunStore(t, func(t *testing.T) cistern.Store {
		if err := r.raw.FlushDB(context.Background()).Err(); err != nil {
			t.Fatalf("FLUSHDB: %v", err)
		}
		return r.storeFor(t, nil)
	})
}

type benchList struct {
	ID    int      `json:"id"`
	Owner int      `json:"owner"`
	Title string   `json:"title"`
	Tasks []string `json:"tasks"`
}

// RNF-06: an L2 hit through the cache — one Redis round trip plus the
// envelope and value decode — against a Redis in a local container. The
// number says more about the network than about cistern; it is the baseline
// the L1 exists to beat.
func BenchmarkCacheGetL2Hit(b *testing.B) {
	r := startRedis(b)
	c, err := cistern.New[string, benchList]("tasks", func(k string) string { return k },
		cistern.WithL2(r.storeFor(b, nil)), cistern.WithTTL(time.Hour))
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
		if _, ok, err := c.Get(ctx, "user:42:list:1"); err != nil || !ok {
			b.Fatalf("Get = %v, %v", ok, err)
		}
	}
}
