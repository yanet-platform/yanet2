package ynpb

import (
	"errors"
	"fmt"
	"slices"
	"strings"

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
	if err := validateFunctionID(m.GetId()); err != nil {
		return err
	}

	var sum uint64
	for idx, functionChain := range m.GetChains() {
		if err := functionChain.Validate(); err != nil {
			return fmt.Errorf("chains[%d]: %w", idx, err)
		}

		weight := functionChain.GetWeight()
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

// Validate checks the chain definition and its individual weight.
func (m *FunctionChain) Validate() error {
	chain := m.GetChain()
	if chain == nil {
		return errors.New("chain is required")
	}
	if err := chain.Validate(); err != nil {
		return fmt.Errorf("chain: %w", err)
	}

	weight := m.GetWeight()
	if weight > commonpb.MaxWeightSum {
		return fmt.Errorf(
			"weight %d must be in range 0..%d",
			weight,
			commonpb.MaxWeightSum,
		)
	}

	return nil
}

func validateFunctionID(id *commonpb.FunctionId) error {
	if id == nil {
		return errors.New("id is required")
	}
	if err := id.Validate(); err != nil {
		return fmt.Errorf("id: %w", err)
	}

	return nil
}

// MaxChainNameLen mirrors the C chain name buffer size, including the
// terminating NUL.
const MaxChainNameLen = 80

func (m *Chain) Validate() error {
	name := m.GetName()
	if name == "" {
		return errors.New("name is required")
	}
	if strings.IndexByte(name, 0) != -1 {
		return errors.New("name must not contain NUL")
	}
	if len(name) >= MaxChainNameLen {
		return fmt.Errorf("name must be shorter than %d bytes", MaxChainNameLen)
	}

	for idx, module := range m.GetModules() {
		if module == nil {
			return fmt.Errorf("modules[%d] is required", idx)
		}
		if err := module.Validate(); err != nil {
			return fmt.Errorf("modules[%d]: %w", idx, err)
		}
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
