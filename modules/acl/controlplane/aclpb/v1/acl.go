package aclpb

import (
	"go.uber.org/zap/zapcore"
)

// AsLogValue implements xgrpc.ProtoLogValue for compact logging.
func (m *UpdateConfigRequest) AsLogValue() any {
	return zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
		enc.AddString("name", m.Name)
		enc.AddString("rules", "<redacted>")
		enc.AddInt("rules_count", len(m.Rules))
		return nil
	})
}
