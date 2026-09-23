// Package bastionguard adapts a bastion circuit breaker to redisstore.Guard
// (RNF-04): every call redisstore makes to Redis goes through the breaker, so
// once Redis has failed enough, reads stop waiting on it at all and cistern
// serves them from the loader (ADR-0002). cistern's core does not depend on
// bastion; this is the whole integration.
package bastionguard

import (
	"context"

	"github.com/JonasBorgesLM/bastion"
)

// Guard runs redisstore's calls through Breaker. A refused call returns
// bastion.ErrOpenState, which reaches cistern's OnError hook as a read error.
type Guard struct {
	Breaker *bastion.Breaker
}

// Do runs op through the breaker. A miss is not a failure: redisstore reports
// it as success, so only real errors count toward tripping.
func (g Guard) Do(ctx context.Context, op func(context.Context) error) error {
	return bastion.Do(ctx, g.Breaker, op)
}
