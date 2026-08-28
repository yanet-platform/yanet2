package ynpb

import (
	"slices"

	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

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
