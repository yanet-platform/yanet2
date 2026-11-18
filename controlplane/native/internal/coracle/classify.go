//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdlib.h>
#include <time.h>
#include "lib/classify/classifiers/line.h"
#include "lib/classify/compiler/line.h"

// The line compile the module SDK declares: a rule is the interval view
// itself, over the domain of one byte.
static void
coracle_get_lines(
	const struct classifier_rule *rule, struct classify_line_ranges *view
) {
	*view = *(const struct classify_line_ranges *)rule;
}

CLASSIFY_LINE_COMPILE(
	coracle_line, struct classify_attr_line, 0x100, coracle_get_lines
)

static int
coracle_line_compile(
	struct memory_context *context,
	const char *name,
	const struct classify_line_ranges **rules,
	uint32_t rule_count,
	struct classify_attr_line *attr,
	struct classifier *stage
) {
	return classify_coracle_line_compile(
		context,
		name,
		(const struct classifier_rule **)rules,
		rule_count,
		attr,
		stage
	);
}

static uint32_t *
coracle_line_values(struct classify_attr_line *attr) {
	return ADDR_OF(&attr->line.values);
}

static const char *
coracle_line_context_name(struct classify_attr_line *attr) {
	return ADDR_OF(&attr->line.memory_context)->name;
}

static struct value_range *
coracle_stage_range(struct classifier *stage, uint64_t idx) {
	return ADDR_OF(&stage->registry.ranges) + idx;
}

static uint32_t *
coracle_range_values(struct value_range *range) {
	return ADDR_OF(&range->values);
}

static void
coracle_line_free(
	struct memory_context *context,
	struct classify_attr_line *attr,
	struct classifier *stage,
	uint32_t rule_count
) {
	classifier_fini(stage, context, rule_count);
	vline_free(&attr->line);
}

// Compiles and frees the line attribute of the rules iterations times
// and returns the nanoseconds taken, or -1 when a compile fails.
static int64_t
coracle_line_compile_loop(
	struct memory_context *context,
	const char *name,
	const struct classify_line_ranges **rules,
	uint32_t rule_count,
	uint64_t iterations
) {
	struct timespec start;
	struct timespec end;
	clock_gettime(CLOCK_MONOTONIC, &start);
	for (uint64_t idx = 0; idx < iterations; ++idx) {
		struct classify_attr_line attr;
		struct classifier stage;
		if (coracle_line_compile(
			    context, name, rules, rule_count, &attr, &stage
		    )) {
			return -1;
		}
		coracle_line_free(context, &attr, &stage, rule_count);
	}
	clock_gettime(CLOCK_MONOTONIC, &end);
	return (int64_t)(end.tv_sec - start.tv_sec) * 1000000000 +
	       (end.tv_nsec - start.tv_nsec);
}
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"
)

// LineRule is one rule of a line compile: its intervals, or none at all
// for a rule absent from the projection.
type LineRule struct {
	Ranges [][2]uint32
	Absent bool
}

// LineResult is what a C line compile produced, copied out of shared
// memory.
type LineResult struct {
	Values      []uint32
	RuleGroups  []uint32
	Groups      [][]uint32
	MaxValue    uint32
	ContextName string
}

// CLineRules holds a ruleset in C memory, as the line compile reads it.
type CLineRules struct {
	pointers **C.struct_classify_line_ranges
	count    C.uint32_t
	blocks   []unsafe.Pointer
}

// NewCLineRules copies rules into C memory; Free releases it.
func NewCLineRules(rules []LineRule) *CLineRules {
	m := &CLineRules{count: C.uint32_t(len(rules))}
	rawPointers := m.alloc(max(len(rules), 1), unsafe.Sizeof(uintptr(0)))
	m.pointers = (**C.struct_classify_line_ranges)(rawPointers)
	pointers := unsafe.Slice(m.pointers, len(rules))
	for idx, rule := range rules {
		if rule.Absent {
			continue
		}
		view := (*C.struct_classify_line_ranges)(m.alloc(1, unsafe.Sizeof(C.struct_classify_line_ranges{})))
		if len(rule.Ranges) > 0 {
			items := unsafe.Slice((*C.struct_classify_line_range)(m.alloc(len(rule.Ranges), unsafe.Sizeof(C.struct_classify_line_range{}))), len(rule.Ranges))
			for rangeIdx, span := range rule.Ranges {
				items[rangeIdx].from = C.uint32_t(span[0])
				items[rangeIdx].to = C.uint32_t(span[1])
			}
			view.items = &items[0]
		}
		view.count = C.uint32_t(len(rule.Ranges))
		pointers[idx] = view
	}
	return m
}

func (m *CLineRules) alloc(count int, size uintptr) unsafe.Pointer {
	block := C.calloc(C.size_t(count), C.size_t(size))
	m.blocks = append(m.blocks, block)
	return block
}

// Free releases the C copy.
func (m *CLineRules) Free() {
	for _, block := range m.blocks {
		C.free(block)
	}
}

// CompileLoop compiles and frees the line attribute of the rules
// iterations times in C, allocating from context, and returns the time
// the compiles took.
func (m *CLineRules) CompileLoop(context unsafe.Pointer, name string, iterations int) (time.Duration, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	elapsed := C.coracle_line_compile_loop((*C.struct_memory_context)(context), cName, m.pointers, m.count, C.uint64_t(iterations))
	if elapsed < 0 {
		return 0, fmt.Errorf("the C line compile failed")
	}
	return time.Duration(elapsed), nil
}

// LineCompile runs the C line compile of the module SDK over rules,
// allocating from context, and returns what it produced after freeing
// it again.
func LineCompile(context unsafe.Pointer, name string, rules []LineRule) (*LineResult, error) {
	cRules := NewCLineRules(rules)
	defer cRules.Free()
	count := int(cRules.count)
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var attr C.struct_classify_attr_line
	var stage C.struct_classifier
	memoryContext := (*C.struct_memory_context)(context)
	rulesPointer := cRules.pointers
	if C.coracle_line_compile(memoryContext, cName, rulesPointer, C.uint32_t(count), &attr, &stage) != 0 {
		return nil, fmt.Errorf("the C line compile failed")
	}
	defer C.coracle_line_free(memoryContext, &attr, &stage, C.uint32_t(count))

	result := &LineResult{
		Values:      append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_line_values(&attr))), attr.line.size)...),
		RuleGroups:  append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(stage.rule_groups)), count)...),
		MaxValue:    uint32(stage.registry.max_value),
		ContextName: C.GoString(C.coracle_line_context_name(&attr)),
	}
	for idx := range uint64(stage.registry.range_count) {
		valueRange := C.coracle_stage_range(&stage, C.uint64_t(idx))
		values := unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_range_values(valueRange))), valueRange.count)
		result.Groups = append(result.Groups, append([]uint32{}, values...))
	}
	return result, nil
}
