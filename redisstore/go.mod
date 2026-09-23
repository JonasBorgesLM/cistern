module github.com/JonasBorgesLM/cistern/redisstore

// No dependency yet: the L2 Store, Bus Pub/Sub and go-redis/testcontainers
// requires arrive in C4 (REQUIREMENTS.md §13). This floor is not a promise
// the way the core's is (ADR-0013) — it moves to whatever go-redis and
// testcontainers-go impose the moment they are added, recorded then as an
// imposition rather than a choice.
go 1.24
