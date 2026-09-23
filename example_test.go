package cistern_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/bus"
	"github.com/JonasBorgesLM/cistern/memory"
)

type listKey struct {
	UserID int
	ListID int
}

type taskList struct {
	Title string   `json:"title"`
	Tasks []string `json:"tasks"`
}

// The key function puts the owner first, so two users can never address the
// same entry (RS-02).
func ExampleNew() {
	l1, err := memory.New(memory.WithMaxEntries(10_000))
	if err != nil {
		panic(err)
	}
	lists, err := cistern.New[listKey, taskList]("tasks",
		func(k listKey) string { return fmt.Sprintf("user:%d:list:%d", k.UserID, k.ListID) },
		cistern.WithL1(l1),
		cistern.WithTTL(30*time.Second),
		cistern.WithJitter(0.1),
	)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	err = lists.Set(ctx, listKey{UserID: 42, ListID: 1}, taskList{Title: "today", Tasks: []string{"review ADR-0005"}})
	if err != nil {
		panic(err)
	}

	own, ok, _ := lists.Get(ctx, listKey{UserID: 42, ListID: 1})
	fmt.Println(own.Title, ok)

	_, ok, _ = lists.Get(ctx, listKey{UserID: 43, ListID: 1})
	fmt.Println("another user's list cached:", ok)
	// Output:
	// today true
	// another user's list cached: false
}

// After writing the source of truth, invalidate rather than Set (ADR-0003).
func ExampleCache_Delete() {
	l1, _ := memory.New()
	cache, _ := cistern.New[string, string]("profiles", func(id string) string { return "user:" + id }, cistern.WithL1(l1), cistern.WithTTL(time.Minute))
	ctx := context.Background()

	_ = cache.Set(ctx, "42", "old display name")

	// ... the profile is updated in the database ...
	if err := cache.Delete(ctx, "42"); err != nil {
		fmt.Println("invalidation failed; the cache may serve stale data:", err)
	}

	_, ok, _ := cache.Get(ctx, "42")
	fmt.Println("cached after invalidation:", ok)
	// Output: cached after invalidation: false
}

// Hooks are where fail-open reads report what they swallowed (ADR-0015).
// Events carry the namespace; the key only with WithHookKeys (RS-08).
func ExampleWithHooks() {
	l1, _ := memory.New()
	var misses int
	cache, _ := cistern.New[string, string]("profiles", func(id string) string { return "user:" + id },
		cistern.WithL1(l1), cistern.WithTTL(time.Minute),
		cistern.WithHooks(cistern.Hooks{
			OnMiss: func(_ context.Context, e cistern.MissEvent) { misses++ },
			OnError: func(_ context.Context, e cistern.ErrorEvent) {
				fmt.Println("swallowed:", e.Op, e.Level, e.Err)
			},
		}))

	_, _, _ = cache.Get(context.Background(), "42")
	fmt.Println("misses:", misses)
	// Output: misses: 1
}

// GetOrLoad is cache-aside in one call: a miss runs the loader and stores
// what it returns (RF-01, RF-05). With WithNegativeTTL, a loader's
// ErrNotFound is remembered too, so a missing key does not reach the source
// on every request (RF-06). TTL overrides the cache's TTL for one entry
// (RF-03).
func ExampleCache_GetOrLoad() {
	l1, _ := memory.New()
	profiles, _ := cistern.New[string, string]("profiles", func(id string) string { return "user:" + id },
		cistern.WithL1(l1), cistern.WithTTL(time.Minute), cistern.WithNegativeTTL(10*time.Second))
	ctx := context.Background()

	queries := 0
	fromDB := func(id string) cistern.Loader[string] {
		return func(context.Context) (string, error) {
			queries++
			if id == "42" {
				return "Ada", nil
			}
			return "", cistern.ErrNotFound // the loader's way to say "no such row"
		}
	}

	for range 2 {
		name, _ := profiles.GetOrLoad(ctx, "42", fromDB("42"), cistern.TTL(5*time.Minute))
		fmt.Println(name)
	}
	for range 2 {
		_, err := profiles.GetOrLoad(ctx, "7", fromDB("7"))
		fmt.Println(errors.Is(err, cistern.ErrNotFound))
	}
	fmt.Println("database queries:", queries)
	// Output:
	// Ada
	// Ada
	// true
	// true
	// database queries: 2
}

// Tags group entries that one write invalidates together (RF-10). The owner
// is in the tag as well as in the key, so invalidating one user's lists never
// touches another's (RS-02, ADR-0005).
func ExampleCache_InvalidateTag() {
	l1, _ := memory.New()
	lists, _ := cistern.New[listKey, string]("tasks",
		func(k listKey) string { return fmt.Sprintf("user:%d:list:%d", k.UserID, k.ListID) },
		cistern.WithL1(l1), cistern.WithTTL(time.Minute),
		cistern.WithTags(func(k listKey) []string { return []string{fmt.Sprintf("user:%d:lists", k.UserID)} }))
	ctx := context.Background()

	_ = lists.Set(ctx, listKey{UserID: 42, ListID: 1}, "today")
	_ = lists.Set(ctx, listKey{UserID: 42, ListID: 2}, "someday")
	_ = lists.Set(ctx, listKey{UserID: 43, ListID: 1}, "groceries")

	// ... user 42 creates a task in the database ...
	if err := lists.InvalidateTag(ctx, "user:42:lists"); err != nil {
		fmt.Println("invalidation failed; the cache may serve stale data:", err)
	}

	for _, k := range []listKey{{42, 1}, {42, 2}, {43, 1}} {
		_, ok, _ := lists.Get(ctx, k)
		fmt.Printf("user %d list %d cached: %v\n", k.UserID, k.ListID, ok)
	}
	// Output:
	// user 42 list 1 cached: false
	// user 42 list 2 cached: false
	// user 43 list 1 cached: true
}

// Each replica has its own L1; a Bus carries invalidations between them, so
// a Delete on one drops the others' copies at once instead of after their L1
// TTL (RF-11, RF-12, ADR-0006). Across processes the Bus is
// redisstore.NewBus; bus.NewLocal connects caches in one process.
func ExampleWithBus() {
	b := bus.NewLocal()
	replica := func() *cistern.Cache[string, string] {
		l1, _ := memory.New()
		c, err := cistern.New[string, string]("profiles", func(id string) string { return "user:" + id },
			cistern.WithL1(l1), cistern.WithTTL(time.Hour), cistern.WithBus(b))
		if err != nil {
			panic(err)
		}
		return c
	}
	a, c := replica(), replica()
	defer a.Close() // ends the cache's subscription
	defer c.Close()
	ctx := context.Background()

	_ = a.Set(ctx, "42", "Ada")
	_ = c.Set(ctx, "42", "Ada")

	// ... the profile changes; replica a handles the write ...
	_ = a.Delete(ctx, "42")

	_, ok, _ := c.Get(ctx, "42")
	fmt.Println("replica c still cached:", ok)
	// Output: replica c still cached: false
}
