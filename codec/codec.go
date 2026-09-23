// Package codec defines how cistern turns values into the bytes it stores,
// and provides the JSON default (RF-13).
//
// Both cache levels hold encoded bytes (ADR-0014), and L2 data is untrusted
// (RS-06): a Codec must decode only into the destination it is given, and
// must never instantiate a type named by the data itself. That rules out
// encodings such as gob with registered interface types.
package codec

import "encoding/json"

// Codec encodes values into bytes and decodes them back.
//
// Implementations must be safe for concurrent use.
type Codec interface {
	// ID names the encoding. It is stored alongside the data, so changing it
	// orphans every entry already written.
	ID() string
	// Marshal encodes v.
	Marshal(v any) ([]byte, error)
	// Unmarshal decodes data into the value v points to, and returns an error
	// for any input that is not exactly one well-formed encoded value. It must
	// not retain data: coalesced callers decode the same bytes (ADR-0007).
	Unmarshal(data []byte, v any) error
}

// JSON is the default Codec, backed by encoding/json.
var JSON Codec = jsonCodec{}

type jsonCodec struct{}

// ID returns "json".
func (jsonCodec) ID() string { return "json" }

// Marshal encodes v with encoding/json.
func (jsonCodec) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes data with encoding/json, which rejects trailing data.
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
