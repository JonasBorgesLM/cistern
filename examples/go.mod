module github.com/JonasBorgesLM/cistern/examples

// Not a published module: it is read and run from a clone, never imported
// (ADR-0001). The floor is 1.25.0 because redisstore's is, an imposition of
// testcontainers (ADR-0013). cistern and redisstore are pinned at a
// pseudo-version of develop until their first tags.
go 1.25.0

require (
	github.com/JonasBorgesLM/bastion v0.2.1
	github.com/JonasBorgesLM/cistern v0.0.0-20260923133010-95390d73f42d
	github.com/JonasBorgesLM/cistern/redisstore v0.0.0-20260923133010-95390d73f42d
	github.com/JonasBorgesLM/crier/core v0.3.0
	github.com/JonasBorgesLM/moat v0.2.0
	github.com/redis/go-redis/v9 v9.22.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
