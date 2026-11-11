package ynpb

// AsLogValue excludes credential material from request logs.
func (m *IntrospectTokenRequest) AsLogValue() any {
	return "<redacted>"
}
