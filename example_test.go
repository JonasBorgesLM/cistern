package cistern_test

import (
	"context"
	"fmt"
	"time"

	"github.com/JonasBorgesLM/cistern"
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
