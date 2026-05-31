package dscppb

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	errConfigNameRequired = status.Error(
		codes.InvalidArgument,
		"config name is required",
	)
)

func (m *ShowConfigRequest) Validate() error {
	if m.Name == "" {
		return errConfigNameRequired
	}

	return nil
}

func (m *AddPrefixesRequest) Validate() error {
	if m.Name == "" {
		return errConfigNameRequired
	}

	return nil
}

func (m *RemovePrefixesRequest) Validate() error {
	if m.Name == "" {
		return errConfigNameRequired
	}

	return nil
}

func (m *SetDscpMarkingRequest) Validate() error {
	if m.Name == "" {
		return errConfigNameRequired
	}

	if m.DscpConfig == nil {
		return status.Error(
			codes.InvalidArgument,
			"DSCP config is required",
		)
	}

	if m.DscpConfig.Flag > 2 {
		return status.Error(
			codes.InvalidArgument,
			"invalid flag value (must be 0, 1, or 2)",
		)
	}

	if m.DscpConfig.Mark > 63 {
		return status.Error(
			codes.InvalidArgument,
			"invalid mark value (must be 0-63)",
		)
	}

	return nil
}
