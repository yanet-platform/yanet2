package forwardpb

import "github.com/yanet-platform/yanet2/common/go/xproto"

// MarshalJSON renders the mode by its declared name, or by its number when
// the value is not declared.
func (m ForwardMode) MarshalJSON() ([]byte, error) {
	return xproto.MarshalEnumJSON(m)
}

// UnmarshalJSON accepts the declared name or the number.
func (m *ForwardMode) UnmarshalJSON(data []byte) error {
	return xproto.UnmarshalEnumJSON(data, m)
}
