package aclpb

import (
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	fwstatemappb "github.com/yanet-platform/yanet2/objects/fwstate/controlplane/fwstatemappb/v1"
)

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *UpdateConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if len(m.GetRules()) == 0 {
		return errors.New("rules must contain at least one rule")
	}
	// A rule without networks matches through the l2 device filter, so
	// the ruleset takes both kinds; vlan ranges from older clients are
	// accepted and ignored.
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
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate accepts an empty name, which asks for the counters of every config.
func (m *GetRulesCountersRequest) Validate() error {
	if m.GetName() == "" {
		return nil
	}

	return commonpb.ValidateModuleName("name", m.GetName())
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
