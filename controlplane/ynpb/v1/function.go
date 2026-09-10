package ynpb

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Validate checks the function identity, required nested messages, and chain
// weights.
func (m *Function) Validate() error {
	if m == nil {
		return errors.New("function is required")
	}
	if m.GetId() == nil {
		return errors.New("function id is required")
	}
	if m.GetId().GetName() == "" {
		return errors.New("function name is required")
	}

	var sum uint64
	for idx, functionChain := range m.GetChains() {
		chain := functionChain.GetChain()
		if chain == nil {
			return errors.New("function chain is required")
		}
		for _, module := range chain.GetModules() {
			if module == nil {
				return errors.New("module id is required")
			}
		}

		weight := functionChain.GetWeight()
		if weight > commonpb.MaxWeightSum {
			return fmt.Errorf("function.chains[%d].weight %d must be in range 0..%d", idx, weight, commonpb.MaxWeightSum)
		}
		if weight > commonpb.MaxWeightSum-sum {
			return fmt.Errorf("function.chains weight sum %d must be in range 0..%d", sum+weight, commonpb.MaxWeightSum)
		}
		sum += weight
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
