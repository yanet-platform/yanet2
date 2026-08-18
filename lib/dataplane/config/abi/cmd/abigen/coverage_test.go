package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateTypeCoverageRejectsEmptySet pins the rejection of an empty set.
func TestValidateTypeCoverageRejectsEmptySet(t *testing.T) {
	require.Error(t, validateTypeCoverage(nil, nil, nil))
}

// TestValidateTypeCoverageRejectsMissingCoreType pins missing cp_module.
func TestValidateTypeCoverageRejectsMissingCoreType(t *testing.T) {
	types := []typeInfo{
		{Kind: "struct", Name: "module"},
		{Kind: "struct", Name: "dp_config"},
		{Kind: "struct", Name: "packet_front"},
		{Kind: "struct", Name: "rte_mbuf"},
	}

	require.Error(t, validateTypeCoverage(types, nil, nil))
}

// TestValidateTypeCoverageRejectsWrongKindForCoreType pins union vs struct.
func TestValidateTypeCoverageRejectsWrongKindForCoreType(t *testing.T) {
	types := []typeInfo{
		{Kind: "union", Name: "module"},
		{Kind: "struct", Name: "dp_config"},
		{Kind: "struct", Name: "packet_front"},
		{Kind: "struct", Name: "cp_module"},
		{Kind: "struct", Name: "rte_mbuf"},
	}

	require.Error(t, validateTypeCoverage(types, nil, nil))
}

func rootTypeFixture(name string) typeInfo {
	return typeInfo{Kind: "struct", Name: name, ByteSize: 8, Members: []typeMember{{Name: "x", Type: "i4"}}}
}

func boundaryEnumFixtures() []typeInfo {
	fixtures := make([]typeInfo, 0, len(requiredBoundaryEnums))
	for _, key := range requiredBoundaryEnums {
		_, name, _ := strings.Cut(key, " ")
		fixtures = append(fixtures, typeInfo{Kind: "enum", Name: name, ByteSize: 4, Enumerators: []enumConstant{{Name: name + "_a", Value: 0}}})
	}

	return fixtures
}

func fullCoreSetFixture() []typeInfo {
	types := []typeInfo{
		rootTypeFixture("module"),
		rootTypeFixture("dp_worker"),
		rootTypeFixture("module_ectx"),
		rootTypeFixture("packet_front"),
		rootTypeFixture("packet"),
		rootTypeFixture("cp_module"),
		rootTypeFixture("dp_config"),
		rootTypeFixture("cp_config"),
		rootTypeFixture("counter_storage"),
		rootTypeFixture("yanet_shm"),
		rootTypeFixture("rte_mbuf"),
		rootTypeFixture("packet_list"),
	}

	return append(types, boundaryEnumFixtures()...)
}

// TestValidateTypeCoverageAcceptsFullCoreSet pins a complete core set.
func TestValidateTypeCoverageAcceptsFullCoreSet(t *testing.T) {
	require.NoError(t, validateTypeCoverage(fullCoreSetFixture(), nil, nil))
}

// TestValidateTypeCoverageRejectsMissingBoundaryEnum pins missing enum.
func TestValidateTypeCoverageRejectsMissingBoundaryEnum(t *testing.T) {
	types := fullCoreSetFixture()[:len(fullCoreSetFixture())-1]
	require.Error(t, validateTypeCoverage(types, nil, nil))
}

// TestValidateTypeCoverageRejectsEmptyBoundaryEnum pins an empty enum.
func TestValidateTypeCoverageRejectsEmptyBoundaryEnum(t *testing.T) {
	types := fullCoreSetFixture()
	types[len(types)-1].Enumerators = nil
	require.Error(t, validateTypeCoverage(types, nil, nil))
}

// TestValidateTypeCoverageRejectsEmptyRoot pins a zero-size, memberless root.
func TestValidateTypeCoverageRejectsEmptyRoot(t *testing.T) {
	types := fullCoreSetFixture()
	types[0] = typeInfo{Kind: "struct", Name: "module"}
	require.Error(t, validateTypeCoverage(types, nil, nil))
}

// TestValidateTypeCoverageRejectsByValueClosureGap pins a missing member.
func TestValidateTypeCoverageRejectsByValueClosureGap(t *testing.T) {
	types := fullCoreSetFixture()
	types[0].Members = []typeMember{{Name: "inner", Type: "struct inner_ctx"}}
	require.Error(t, validateTypeCoverage(types, nil, nil))
}

// TestValidateTypeCoverageAcceptsPointerOnlyClosureGap pins pointer-only gaps.
func TestValidateTypeCoverageAcceptsPointerOnlyClosureGap(t *testing.T) {
	types := fullCoreSetFixture()
	types[0].Members = append(types[0].Members, typeMember{Name: "opaque", Type: "*struct opaque_handle"})
	require.NoError(t, validateTypeCoverage(types, nil, nil))
}

// TestValidateTypeCoverageRejectsFunctionByValueClosureGap pins rejection.
func TestValidateTypeCoverageRejectsFunctionByValueClosureGap(t *testing.T) {
	types := fullCoreSetFixture()
	functions := []functionInfo{
		{Name: "packet_info", Return: "struct packet_info", Params: []string{"*struct packet"}},
	}

	require.Error(t, validateTypeCoverage(types, functions, nil))
}

// TestValidateTypeCoverageAcceptsFunctionPointerOnlyClosureGap pins.
func TestValidateTypeCoverageAcceptsFunctionPointerOnlyClosureGap(t *testing.T) {
	types := fullCoreSetFixture()
	functions := []functionInfo{
		{Name: "packet_touch", Return: "v", Params: []string{"*struct packet"}},
	}

	require.NoError(t, validateTypeCoverage(types, functions, nil))
}

// TestValidateTypeCoverageAcceptsByValueClosureGapOnExcludedName pins.
func TestValidateTypeCoverageAcceptsByValueClosureGapOnExcludedName(t *testing.T) {
	types := fullCoreSetFixture()
	functions := []functionInfo{
		{Name: "packet_info", Return: "struct packet_info", Params: []string{"*struct packet"}},
	}

	excluded := map[string]bool{"struct packet_info": true}
	require.NoError(t, validateTypeCoverage(types, functions, excluded))
}

// TestValidateTypeCoverageRejectsByValueClosureGapOnNeverSeenName pins.
func TestValidateTypeCoverageRejectsByValueClosureGapOnNeverSeenName(t *testing.T) {
	types := fullCoreSetFixture()
	functions := []functionInfo{
		{Name: "packet_info", Return: "struct packet_info", Params: []string{"*struct packet"}},
	}

	excluded := map[string]bool{"struct some_other_type": true}
	require.Error(t, validateTypeCoverage(types, functions, excluded))
}
