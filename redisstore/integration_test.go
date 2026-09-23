//go:build integration

// The integration suite runs against a real Redis through testcontainers,
// never miniredis (RNF-07): a fake proves nothing about the server that has to
// honour TTLs, ACLs and pauses. It is behind the "integration" build tag, so a
// plain `go test ./...` never needs Docker; the CI job that runs it says so
// when it skips.
package redisstore_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

// redisImage is pinned: a floating tag makes today's green run say nothing
// about tomorrow's.
const redisImage = "redis:7.4-alpine"

type testRedis struct {
	container *tcredis.RedisContainer
	addr      string
	raw       *redis.Client // setup and assertions outside the Store contract
}

func startRedis(t *testing.T) *testRedis {
	t.Helper()
	ctx := context.Background()
	container, err := tcredis.Run(ctx, redisImage)
	if err != nil {
		t.Fatalf("starting redis: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	addr, err := container.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("redis endpoint: %v", err)
	}
	raw := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = raw.Close() })
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := raw.Ping(waitCtx).Err(); err != nil {
		t.Fatalf("redis not ready: %v", err)
	}
	return &testRedis{container: container, addr: addr, raw: raw}
}

// storeFor returns a Store over a default go-redis client — only what New
// requires is set — so the suite exercises what a consumer would build.
func (r *testRedis) storeFor(t *testing.T, opts *redis.Options, storeOpts ...redisstore.Option) *redisstore.Store {
	t.Helper()
	if opts == nil {
		opts = &redis.Options{}
	}
	opts.Addr = r.addr
	opts.ContextTimeoutEnabled = true
	c := redis.NewClient(opts)
	t.Cleanup(func() { _ = c.Close() })
	s, err := redisstore.New(c, storeOpts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreAgainstRealRedis(t *testing.T) {
	r := startRedis(t)
	s := r.storeFor(t, nil)
	ctx := context.Background()

	if _, ok, err := s.Get(ctx, "cistern:v1:t:-:k:missing"); err != nil || ok {
		t.Fatalf("Get of a missing key = %v, %v; want false, nil", ok, err)
	}
	if err := s.Set(ctx, "cistern:v1:t:-:k:a", []byte("one"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "cistern:v1:t:-:k:a", []byte("two"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Get(ctx, "cistern:v1:t:-:k:a"); err != nil || !ok || string(v) != "two" {
		t.Fatalf("Get = %q, %v, %v; want the replaced value", v, ok, err)
	}
	if ttl := r.raw.PTTL(ctx, "cistern:v1:t:-:k:a").Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("Redis holds the key with TTL %v, want at most the minute given", ttl)
	}
	if err := s.Delete(ctx, "cistern:v1:t:-:k:a"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(ctx, "cistern:v1:t:-:k:a"); ok {
		t.Fatal("Get after Delete found the key")
	}
	if err := s.Delete(ctx, "cistern:v1:t:-:k:a"); err != nil {
		t.Fatalf("Delete of a missing key: %v", err)
	}

	// A sub-millisecond TTL, which cistern's jitter can produce, is stored at
	// Redis's resolution rather than rejected by it.
	if err := s.Set(ctx, "cistern:v1:t:-:k:tiny", []byte("x"), time.Nanosecond); err != nil {
		t.Fatalf("Set with a 1ns TTL: %v", err)
	}
}

func TestEntriesExpireInRedis(t *testing.T) {
	r := startRedis(t)
	s := r.storeFor(t, nil)
	ctx := context.Background()
	if err := s.Set(ctx, "cistern:v1:t:-:k:short", []byte("x"), 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(ctx, "cistern:v1:t:-:k:short"); !ok {
		t.Fatal("entry missing right after Set")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, _ := s.Get(ctx, "cistern:v1:t:-:k:short"); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("entry still present long after its TTL")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RNF-03, T-12, sapper scenario 4 in miniature: a Redis that stops answering
// costs a call the store's timeout.
func TestPausedRedisIsBoundedByTheTimeout(t *testing.T) {
	r := startRedis(t)
	s := r.storeFor(t, nil, redisstore.WithTimeout(50*time.Millisecond))
	ctx := context.Background()
	if err := s.Set(ctx, "cistern:v1:t:-:k:a", []byte("x"), time.Minute); err != nil {
		t.Fatal(err) // warms the connection
	}
	if err := r.raw.Do(ctx, "CLIENT", "PAUSE", "2000", "ALL").Err(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, err := s.Get(ctx, "cistern:v1:t:-:k:a")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Get against a paused Redis returned after %v", elapsed)
	}
	if err == nil {
		t.Fatal("Get against a paused Redis returned no error")
	}
}

// RS-09: the ACL documented in the README is enough for a default go-redis
// client, and no more than it: keys outside cistern's prefix and
// administrative commands are refused.
func TestMinimalACLIsSufficient(t *testing.T) {
	r := startRedis(t)
	ctx := context.Background()
	fields := strings.Fields(minimalACL)
	args := make([]any, 0, len(fields))
	for _, f := range fields {
		args = append(args, f)
	}
	if err := r.raw.Do(ctx, args...).Err(); err != nil {
		t.Fatalf("creating the documented ACL user: %v", err)
	}
	s := r.storeFor(t, &redis.Options{Username: "cistern", Password: "s3cret-for-tests"})

	if err := s.Set(ctx, "cistern:v1:t:-:k:a", []byte("x"), time.Minute); err != nil {
		t.Fatalf("Set as the ACL user: %v", err)
	}
	if _, ok, err := s.Get(ctx, "cistern:v1:t:-:k:a"); err != nil || !ok {
		t.Fatalf("Get as the ACL user = %v, %v", ok, err)
	}
	if err := s.Delete(ctx, "cistern:v1:t:-:k:a"); err != nil {
		t.Fatalf("Delete as the ACL user: %v", err)
	}

	if err := s.Set(ctx, "moat:ratelimit:1.2.3.4", []byte("x"), time.Minute); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("Set outside cistern's prefix: err = %v, want NOPERM", err)
	}
	limited := redis.NewClient(&redis.Options{Addr: r.addr, Username: "cistern", Password: "s3cret-for-tests"})
	defer limited.Close()
	if err := limited.FlushAll(ctx).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("FLUSHALL as the ACL user: err = %v, want NOPERM", err)
	}
}

// The C4 exit criterion (REQUIREMENTS.md §13) and ADR-0002: with Redis killed
// mid-run, reads keep working — served by the loader, each bounded by the
// store's timeout — and invalidation reports the failure instead of hiding it.
func TestCacheKeepsServingWhenRedisDies(t *testing.T) {
	r := startRedis(t)
	l2 := r.storeFor(t, nil, redisstore.WithTimeout(100*time.Millisecond))
	c, err := cistern.New[string, string]("tasks", func(k string) string { return k },
		cistern.WithL2(l2), cistern.WithTTL(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var loads atomic.Int32
	load := func(context.Context) (string, error) {
		loads.Add(1)
		return "from source", nil
	}

	if v, err := c.GetOrLoad(ctx, "user:42:list", load); err != nil || v != "from source" {
		t.Fatalf("GetOrLoad with Redis up = %q, %v", v, err)
	}
	if v, ok, err := c.Get(ctx, "user:42:list"); err != nil || !ok || v != "from source" {
		t.Fatalf("Get with Redis up = %q, %v, %v; want a hit", v, ok, err)
	}

	stop := 5 * time.Second
	if err := r.container.Stop(ctx, &stop); err != nil {
		t.Fatalf("stopping redis: %v", err)
	}

	for i := range 5 {
		start := time.Now()
		v, err := c.GetOrLoad(ctx, "user:42:list", load)
		if err != nil || v != "from source" {
			t.Fatalf("GetOrLoad %d with Redis down = %q, %v; want the loader's value", i, v, err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("GetOrLoad %d with Redis down took %v", i, elapsed)
		}
	}
	if v, ok, err := c.Get(ctx, "user:42:list"); err != nil || ok {
		t.Fatalf("Get with Redis down = %q, %v, %v; want a plain miss", v, ok, err)
	}
	if err := c.Delete(ctx, "user:42:list"); err == nil {
		t.Fatal("Delete with Redis down returned nil; invalidation must fail loud")
	}
	if got := loads.Load(); got != 6 {
		t.Fatalf("loader called %d times, want 1 while up and 5 while down", got)
	}
}
