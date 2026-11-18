//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include "common/value.h"
#include "lib/classify/compiler/declare.h"
#include "lib/classify/compiler/helper.h"

extern uint64_t coracle_heap_bytes;

// Builds a stage in shared memory from its groups laid out flat: the
// values of every group, group after group, and where each begins.
static int
coracle_stage_build(
	struct classifier *stage,
	struct memory_context *context,
	const uint32_t *rule_groups,
	uint32_t rule_count,
	const uint32_t *values,
	const uint32_t *starts,
	uint32_t group_count,
	uint32_t max_value
) {
	if (classifier_init(stage, context, rule_count)) {
		return -1;
	}
	if (rule_count) {
		memcpy(stage->rule_groups,
		       rule_groups,
		       rule_count * sizeof(uint32_t));
	}
	if (value_registry_init(&stage->registry, context, "coracle:stage")) {
		return -1;
	}
	for (uint32_t group = 0; group < group_count; ++group) {
		if (value_registry_start(&stage->registry)) {
			return -1;
		}
		for (uint32_t idx = starts[group]; idx < starts[group + 1];
		     ++idx) {
			if (value_registry_collect(&stage->registry, values[idx])) {
				return -1;
			}
		}
	}
	stage->registry.max_value = max_value;
	return 0;
}

static uint32_t *
coracle_table_row(struct value_table *table, uint32_t v) {
	return value_table_get_ptr(table, v, 0);
}

static struct value_range *
coracle_joint_range(struct classifier *joint, uint64_t idx) {
	return ADDR_OF(&joint->registry.ranges) + idx;
}

static uint32_t *
coracle_joint_values(struct value_range *range) {
	return ADDR_OF(&range->values);
}

static const char *
coracle_table_context_name(struct value_table *table) {
	return ADDR_OF(&table->memory_context)->name;
}

// Joins two stages and frees the join iterations times and returns the
// nanoseconds taken, or -1 when a join fails.
static int64_t
coracle_join_loop(
	struct memory_context *context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	uint64_t iterations
) {
	struct timespec start;
	struct timespec end;
	clock_gettime(CLOCK_MONOTONIC, &start);
	for (uint64_t idx = 0; idx < iterations; ++idx) {
		struct value_table table;
		struct classifier joint;
		if (classify_join(
			    context, name, left, right, rule_count, &table, &joint
		    )) {
			return -1;
		}
		value_table_free(&table);
		classifier_fini(&joint, context, rule_count);
	}
	clock_gettime(CLOCK_MONOTONIC, &end);
	return (int64_t)(end.tv_sec - start.tv_sec) * 1000000000 +
	       (end.tv_nsec - start.tv_nsec);
}
*/
import "C"

import (
	"fmt"
	"slices"
	"time"
	"unsafe"
)

// Stage is a classification stage laid out as the native compile lays it
// out: the group of every rule and the values of every group, group
// after group.
type Stage struct {
	RuleGroups []uint32
	Values     []uint32
	Starts     []uint32
	MaxValue   uint32
}

// CStage is a stage built in shared memory for the C join.
type CStage struct {
	context *C.struct_memory_context
	stage   *C.struct_classifier
	rules   C.uint32_t
}

// NewCStage builds a stage in shared memory, allocating from context;
// Free releases it.
func NewCStage(context unsafe.Pointer, stage Stage) (*CStage, error) {
	m := &CStage{
		context: (*C.struct_memory_context)(context),
		stage:   (*C.struct_classifier)(C.calloc(1, C.size_t(unsafe.Sizeof(C.struct_classifier{})))),
		rules:   C.uint32_t(len(stage.RuleGroups)),
	}
	// C reads the slices only during the call, so they stay Go memory.
	ruleGroups := append(slices.Clip(stage.RuleGroups), 0)
	values := append(slices.Clip(stage.Values), 0)
	if C.coracle_stage_build(
		m.stage,
		m.context,
		(*C.uint32_t)(&ruleGroups[0]),
		m.rules,
		(*C.uint32_t)(&values[0]),
		(*C.uint32_t)(&stage.Starts[0]),
		C.uint32_t(len(stage.Starts)-1),
		C.uint32_t(stage.MaxValue),
	) != 0 {
		m.Free()
		return nil, fmt.Errorf("build the C stage")
	}
	return m, nil
}

// Free releases the stage.
func (m *CStage) Free() {
	C.classifier_fini(m.stage, m.context, m.rules)
	C.free(unsafe.Pointer(m.stage))
}

// JoinResult is what a C join produced, copied out of shared memory.
type JoinResult struct {
	// Table holds the class of every cell, row after row.
	Table       [][]uint32
	RuleGroups  []uint32
	Groups      [][]uint32
	MaxValue    uint32
	ContextName string
}

// Join runs the C join of the module SDK over two stages, allocating
// from context, and returns what it produced after freeing it again.
func Join(context unsafe.Pointer, name string, left, right *CStage) (*JoinResult, error) {
	memoryContext := (*C.struct_memory_context)(context)
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	// The table and the stage hold relative pointers, so they stay put
	// in C memory.
	table := (*C.struct_value_table)(C.calloc(1, C.size_t(unsafe.Sizeof(C.struct_value_table{}))))
	defer C.free(unsafe.Pointer(table))
	joint := (*C.struct_classifier)(C.calloc(1, C.size_t(unsafe.Sizeof(C.struct_classifier{}))))
	defer C.free(unsafe.Pointer(joint))
	if C.classify_join(memoryContext, cName, left.stage, right.stage, left.rules, table, joint) != 0 {
		return nil, fmt.Errorf("the C join failed")
	}
	defer C.value_table_free(table)
	defer C.classifier_fini(joint, memoryContext, left.rules)

	result := &JoinResult{
		RuleGroups:  append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(joint.rule_groups)), int(left.rules))...),
		MaxValue:    uint32(joint.registry.max_value),
		ContextName: C.GoString(C.coracle_table_context_name(table)),
	}
	for v := range uint32(table.v_dim) {
		row := unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_table_row(table, C.uint32_t(v)))), table.h_dim)
		result.Table = append(result.Table, append([]uint32{}, row...))
	}
	for idx := range uint64(joint.registry.range_count) {
		valueRange := C.coracle_joint_range(joint, C.uint64_t(idx))
		values := unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_joint_values(valueRange))), valueRange.count)
		result.Groups = append(result.Groups, append([]uint32{}, values...))
	}
	return result, nil
}

// JoinLoop joins two stages and frees the join iterations times in C,
// allocating from context, and returns the time the joins took.
func JoinLoop(context unsafe.Pointer, name string, left, right *CStage, iterations int) (time.Duration, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	elapsed := C.coracle_join_loop((*C.struct_memory_context)(context), cName, left.stage, right.stage, left.rules, C.uint64_t(iterations))
	if elapsed < 0 {
		return 0, fmt.Errorf("the C join failed")
	}
	return time.Duration(elapsed), nil
}

// HeapBytes returns the bytes the C classify helpers requested from the
// heap so far, every growth counted whole.
func HeapBytes() uint64 {
	return uint64(C.coracle_heap_bytes)
}
