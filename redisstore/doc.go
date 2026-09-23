// Package redisstore is cistern's Redis-backed L2 Store (RF-07).
//
// It is a separate module so that go-redis never enters the dependency graph
// of a consumer that runs L1-only or brings its own Store (ADR-0001).
//
// It targets standalone Redis and Sentinel, not Cluster, and says so in its
// types: New takes a *redis.Client (ADR-0012). Every call is bounded by a
// per-call timeout and runs through an optional Guard such as a circuit
// breaker (RNF-03, RNF-04); the Store reports errors, and cistern's read path
// turns them into misses (ADR-0002).
//
// Operating Redis for it — the minimal ACL, TLS and maxmemory-policy — is in
// the module's README.
package redisstore
