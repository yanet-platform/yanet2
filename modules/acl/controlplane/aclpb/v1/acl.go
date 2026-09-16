package aclpb

import (
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"

	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

func (m *ShowConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *UpdateConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if len(m.GetRules()) == 0 {
		return errors.New("rules must contain at least one rule")
	}
	if m.GetSyncConfig() != nil {
		return errors.New("sync_config belongs to fwstate")
	}
	if err := validateOptionalMapName(
		"fwtable_name_v4",
		m.GetFwtableNameV4(),
	); err != nil {
		return err
	}
	if err := validateOptionalMapName(
		"fwtable_name_v6",
		m.GetFwtableNameV6(),
	); err != nil {
		return err
	}

	return nil
}

func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *GetMetricsRulesRequest) Validate() error {
	selectors := [][2]string{
		{"config", m.GetConfig()},
		{"device", m.GetDevice()},
		{"pipeline", m.GetPipeline()},
		{"function", m.GetFunction()},
		{"chain", m.GetChain()},
	}

	for _, selector := range selectors {
		field, value := selector[0], selector[1]
		if value == "" || value == "*" {
			continue
		}
		if strings.IndexByte(value, 0) != -1 {
			return fmt.Errorf("%s must not contain NUL", field)
		}
		if len(value) >= ynpb.MaxCounterTagValueLen {
			return fmt.Errorf(
				"%s must be shorter than %d bytes",
				field,
				ynpb.MaxCounterTagValueLen,
			)
		}
	}

	return nil
}

func validateOptionalMapName(field, name string) error {
	if name == "" {
		return nil
	}

	return fwstatemappb.ValidateMapNameField(field, name)
}

// AsLogValue implements xgrpc.ProtoLogValue for compact logging.
func (m *UpdateConfigRequest) AsLogValue() any {
	return zapcore.ObjectMarshalerFunc(func(enc zapcore.ObjectEncoder) error {
		enc.AddString("name", m.Name)
		enc.AddString("rules", "<redacted>")
		enc.AddInt("rules_count", len(m.Rules))
		return nil
	})
}
