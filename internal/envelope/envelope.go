// Package envelope is the storage format of every cistern entry (ADR-0009):
// a fixed header — version, flags, logical expiry, codec id — followed by the
// encoded value. Decoding validates every field, because what comes back from
// a store is untrusted (RS-06).
package envelope

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// Version is the format version this package writes and reads.
const Version = 1

const (
	flagAbsent   = 1 << 0
	knownFlags   = flagAbsent
	headerLen    = 11 // version, flags, 8-byte expiry, codec id length
	maxCodecID   = 32
	expiryOffset = 2
	codecOffset  = 10
)

// ErrMalformed reports bytes that are not a valid envelope. Callers treat it
// as a miss.
var ErrMalformed = errors.New("envelope: malformed")

// Entry is a decoded envelope.
type Entry struct {
	Absent  bool      // a remembered absence (negative caching); Payload is empty
	Expires time.Time // logical expiry
	Codec   string    // id of the codec that produced Payload
	Payload []byte    // the encoded value; empty if and only if Absent
}

// Encode returns e in the ADR-0009 layout, or an error if e cannot be
// represented.
func Encode(e Entry) ([]byte, error) {
	if err := check(e.Absent, e.Expires.UnixNano(), len(e.Codec), len(e.Payload)); err != nil {
		return nil, fmt.Errorf("envelope: cannot encode: %w", err)
	}
	var flags byte
	if e.Absent {
		flags |= flagAbsent
	}
	out := make([]byte, 0, headerLen+len(e.Codec)+len(e.Payload))
	out = append(out, Version, flags)
	out = binary.BigEndian.AppendUint64(out, uint64(e.Expires.UnixNano())) // #nosec G115 -- check rejected non-positive expiries
	out = append(out, byte(len(e.Codec)))                                  // #nosec G115 -- check bounded it to maxCodecID
	out = append(out, e.Codec...)
	return append(out, e.Payload...), nil
}

// Decode parses data, rejecting anything Encode would not have produced or a
// payload longer than maxPayload. The returned Payload aliases data.
func Decode(data []byte, maxPayload int) (Entry, error) {
	if len(data) < headerLen {
		return Entry{}, fmt.Errorf("%w: %d bytes is shorter than the header", ErrMalformed, len(data))
	}
	if data[0] != Version {
		return Entry{}, fmt.Errorf("%w: unknown version %d", ErrMalformed, data[0])
	}
	flags := data[1]
	if flags&^knownFlags != 0 {
		return Entry{}, fmt.Errorf("%w: unknown flags %#x", ErrMalformed, flags)
	}
	expiry := int64(binary.BigEndian.Uint64(data[expiryOffset:codecOffset])) // #nosec G115 -- a negative result is rejected by check
	codecLen := int(data[codecOffset])
	if len(data) < headerLen+codecLen {
		return Entry{}, fmt.Errorf("%w: codec id runs past the end", ErrMalformed)
	}
	payload := data[headerLen+codecLen:]
	absent := flags&flagAbsent != 0
	if err := check(absent, expiry, codecLen, len(payload)); err != nil {
		return Entry{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if len(payload) > maxPayload {
		return Entry{}, fmt.Errorf("%w: payload of %d bytes exceeds the limit of %d", ErrMalformed, len(payload), maxPayload)
	}
	return Entry{
		Absent:  absent,
		Expires: time.Unix(0, expiry),
		Codec:   string(data[headerLen : headerLen+codecLen]),
		Payload: payload,
	}, nil
}

func check(absent bool, expiry int64, codecLen, payloadLen int) error {
	switch {
	case expiry <= 0:
		return errors.New("expiry must be positive")
	case codecLen < 1 || codecLen > maxCodecID:
		return fmt.Errorf("codec id length %d is outside 1..%d", codecLen, maxCodecID)
	case absent && payloadLen != 0:
		return errors.New("an absence carries no payload")
	case !absent && payloadLen == 0:
		return errors.New("a value needs a payload")
	}
	return nil
}
