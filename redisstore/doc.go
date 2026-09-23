// Package redisstore will provide cistern's Redis-backed L2 Store and the
// Redis Pub/Sub implementation of its invalidation Bus (RF-11).
//
// It is a separate module so that go-redis never enters the dependency graph
// of a consumer that runs L1-only or brings its own Store (ADR-0001). The
// implementation arrives in phase C4.
package redisstore
