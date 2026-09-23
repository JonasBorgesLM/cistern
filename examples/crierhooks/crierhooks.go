// Package crierhooks exports cistern's hooks to crier (RF-15, ADR-0015): the
// errors fail-open swallows become warnings in the host's log pipeline, which
// is how a Redis outage becomes visible rather than just slower.
package crierhooks

import (
	"context"
	"time"

	"github.com/JonasBorgesLM/crier/core"

	"github.com/JonasBorgesLM/cistern"
)

// Hooks returns hooks that log every swallowed error to c. The record
// carries the namespace, operation, level and error — and the key only if the
// cache was built with cistern.WithHookKeys (RS-08). crier admits records
// without blocking, as hooks require; a record it refuses is dropped.
func Hooks(c *core.Crier) cistern.Hooks {
	return cistern.Hooks{
		OnError: func(ctx context.Context, e cistern.ErrorEvent) {
			attrs := map[string]any{
				"cistern.namespace": e.Namespace,
				"cistern.op":        string(e.Op),
				"cistern.level":     e.Level.String(),
				"error":             e.Err.Error(),
			}
			if e.Key != "" {
				attrs["cistern.key"] = e.Key
			}
			if err := c.Log(ctx, core.LogRecord{
				Timestamp:    time.Now(),
				Severity:     core.SeverityWarn,
				SeverityText: "WARN",
				Body:         "cistern: a cache operation failed and was treated as a miss",
				Attributes:   attrs,
			}); err != nil {
				return // hooks must not block or fail the read
			}
		},
	}
}
