package ynpb

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *GetFunctionRequest) Validate() error {
	return validateFunctionID(m.GetId())
}

func (m *UpdateFunctionRequest) Validate() error {
	if m.GetFunction() == nil {
		return errors.New("function is required")
	}
	if err := m.GetFunction().Validate(); err != nil {
		return fmt.Errorf("function: %w", err)
	}

	return nil
}

func (m *DeleteFunctionRequest) Validate() error {
	return validateFunctionID(m.GetId())
}

// Validate checks the function identity, required nested messages, and chain
// weights.
func (m *Function) Validate() error {
	if m == nil {
		return errors.New("function is required")
	}
	if err := validateFunctionID(m.GetId()); err != nil {
		return err
	}

	var sum uint64
	for idx, functionChain := range m.GetChains() {
		chain := functionChain.GetChain()
		if chain == nil {
			return fmt.Errorf("chains[%d].chain is required", idx)
		}
		for moduleIndex, module := range chain.GetModules() {
			if module == nil {
				return fmt.Errorf(
					"chains[%d].chain.modules[%d] is required",
					idx,
					moduleIndex,
				)
			}
		}

		weight := functionChain.GetWeight()
		if weight > commonpb.MaxWeightSum {
			return fmt.Errorf(
				"chains[%d].weight %d must be in range 0..%d",
				idx,
				weight,
				commonpb.MaxWeightSum,
			)
		}
		if weight > commonpb.MaxWeightSum-sum {
			return fmt.Errorf(
				"chains weight sum %d must be in range 0..%d",
				sum+weight,
				commonpb.MaxWeightSum,
			)
		}
		sum += weight
	}
	return nil
}

func validateFunctionID(id *commonpb.FunctionId) error {
	if id == nil {
		return errors.New("id is required")
	}
	if id.GetName() == "" {
		return errors.New("id.name is required")
	}

	return nil
}

// Equal reports whether both functions carry the same definition.
func (m *Function) Equal(other *Function) bool {
	return proto.Equal(m, other)
}

// WithoutModules returns a deep copy of the function with the modules of
// the given type dropped from every chain.
//
// A nil function yields nil.
func (m *Function) WithoutModules(moduleType string) *Function {
	function := proto.Clone(m).(*Function)
	for _, chain := range function.GetChains() {
		if chain := chain.GetChain(); chain != nil {
			chain.Modules = slices.DeleteFunc(chain.Modules, func(module *commonpb.ModuleId) bool {
				return module.GetType() == moduleType
			})
		}
	}
	return function
}
