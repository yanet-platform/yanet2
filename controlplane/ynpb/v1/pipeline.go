package ynpb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *GetPipelineRequest) Validate() error {
	return validatePipelineID(m.GetId())
}

func (m *UpdatePipelineRequest) Validate() error {
	if m.GetPipeline() == nil {
		return errors.New("pipeline is required")
	}
	if err := m.GetPipeline().Validate(); err != nil {
		return fmt.Errorf("pipeline: %w", err)
	}

	return nil
}

func (m *DeletePipelineRequest) Validate() error {
	return validatePipelineID(m.GetId())
}

func (m *Pipeline) Validate() error {
	if m == nil {
		return errors.New("pipeline is required")
	}
	if err := validatePipelineID(m.GetId()); err != nil {
		return err
	}
	for idx, functionID := range m.GetFunctions() {
		if functionID == nil {
			return fmt.Errorf("functions[%d] is required", idx)
		}
	}

	return nil
}

func validatePipelineID(id *commonpb.PipelineId) error {
	if id == nil {
		return errors.New("id is required")
	}
	name := id.GetName()
	if name == "" {
		return errors.New("id.name is required")
	}
	if strings.IndexByte(name, 0) != -1 {
		return errors.New("id.name must not contain NUL")
	}
	if len(name) >= commonpb.MaxPipelineNameLen {
		return fmt.Errorf("id.name must be shorter than %d bytes", commonpb.MaxPipelineNameLen)
	}

	return nil
}
