package memory

import "time"

// SetNow replaces the store's clock, so TTL tests advance time instead of
// sleeping.
func (s *Store) SetNow(now func() time.Time) { s.now = now }
