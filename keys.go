package cistern

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// MaxKeyBytes is the longest consumer key stored verbatim (ADR-0008). A longer
// key is refused with ErrInvalidKey unless the cache was built with
// WithKeyHashing.
const MaxKeyBytes = 256

// physicalKey validates the consumer key for k and returns the key every
// level stores it under: cistern:v1:<namespace>:<generations>:<kind>:<key>
// (ADR-0008).
func (c *Cache[K, V]) physicalKey(k K) (string, error) {
	key := c.key(k)
	if err := validateKey(key); err != nil {
		return "", err
	}
	if len(key) <= MaxKeyBytes {
		return c.prefix + "k:" + key, nil
	}
	if !c.hashKeys {
		return "", fmt.Errorf("%w: %d bytes exceeds %d (see WithKeyHashing)", ErrInvalidKey, len(key), MaxKeyBytes)
	}
	sum := sha256.Sum256([]byte(key))
	return c.prefix + "h:" + hex.EncodeToString(sum[:]), nil
}

// validateKey enforces RS-03 apart from the length limit, which depends on
// hashing: non-empty, valid UTF-8, and no control characters, so a key can
// never inject into a log line or be read two ways.
func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty", ErrInvalidKey)
	}
	if !utf8.ValidString(key) {
		return fmt.Errorf("%w: not valid UTF-8", ErrInvalidKey)
	}
	for i, r := range key {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: control character %U at byte %d", ErrInvalidKey, r, i)
		}
	}
	return nil
}
