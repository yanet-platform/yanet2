package commonpb

import (
	"encoding/json"
	"math"
)

// MarshalJSON renders histogram buckets for the gateway's HTTP translation
// layer, which encodes gRPC responses with plain encoding/json.
//
// That encoder rejects non-finite floats, and the final bucket of every
// histogram carries an upper bound of +Inf (see common/go/metrics). The
// binary protobuf wire format keeps non-finite bounds untouched, since
// Prometheus export depends on it, but the JSON rendering maps them to
// null, mirroring the Rust CLI's serde output for --format json.
func (m *Bucket) MarshalJSON() ([]byte, error) {
	type shadow struct {
		Count      uint64   `json:"count"`
		UpperBound *float64 `json:"upper_bound"`
	}

	s := shadow{Count: m.Count}
	if !math.IsInf(m.UpperBound, 0) && !math.IsNaN(m.UpperBound) {
		upperBound := m.UpperBound
		s.UpperBound = &upperBound
	}

	return json.Marshal(s)
}
