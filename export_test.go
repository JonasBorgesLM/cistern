package cistern

// SetRand replaces the cache's jitter source, so tests choose the draw.
func SetRand[K comparable, V any](c *Cache[K, V], rand func() float64) { c.rand = rand }
