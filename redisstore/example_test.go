package redisstore_test

import (
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/JonasBorgesLM/cistern"
	"github.com/JonasBorgesLM/cistern/memory"
	"github.com/JonasBorgesLM/cistern/redisstore"
)

// A two-level cache: an in-process L1 in front of Redis. The L1 TTL is the
// worst-case staleness between replicas (ADR-0004), so it is short.
func ExampleNew() {
	client := redis.NewClient(&redis.Options{
		Addr:                  "localhost:6379",
		ContextTimeoutEnabled: true, // required: without it per-call timeouts are ignored
	})
	defer client.Close()

	l2, err := redisstore.New(client, redisstore.WithTimeout(50*time.Millisecond))
	if err != nil {
		panic(err)
	}
	l1, err := memory.New(memory.WithMaxEntries(10_000))
	if err != nil {
		panic(err)
	}
	lists, err := cistern.New[int, []string]("tasks",
		func(userID int) string { return fmt.Sprintf("user:%d:lists", userID) },
		cistern.WithL1(l1),
		cistern.WithL2(l2),
		cistern.WithTTL(5*time.Minute),
		cistern.WithL1TTL(5*time.Second),
	)
	if err != nil {
		panic(err)
	}
	_ = lists
}
