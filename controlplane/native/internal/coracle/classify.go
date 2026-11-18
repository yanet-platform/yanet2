//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdlib.h>
#include <time.h>
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/u16_ranges.h"

// The line and port compiles the module SDK declares: a rule is the
// interval view itself.
static void
coracle_get_lines(
	const struct classifier_rule *rule, struct classify_line_ranges *view
) {
	*view = *(const struct classify_line_ranges *)rule;
}

static void
coracle_get_ports(
	const struct classifier_rule *rule, struct filter_u16_ranges *view
) {
	*view = *(const struct filter_u16_ranges *)rule;
}

CLASSIFY_LINE_COMPILE(
	coracle_line, struct classify_attr_line, 0x100, coracle_get_lines
)
CLASSIFY_U16_RANGES_COMPILE(
	coracle_port, struct classify_attr_port, coracle_get_ports
)

enum coracle_attr_kind { CORACLE_LINE, CORACLE_PORT };

// One compiled attribute of either kind: both embed a single line.
union coracle_attr {
	struct classify_attr_line line;
	struct classify_attr_port port;
};

static int
coracle_attr_compile(
	enum coracle_attr_kind kind,
	struct memory_context *context,
	const char *name,
	void **rules,
	uint32_t rule_count,
	union coracle_attr *attr,
	struct classifier *stage
) {
	const struct classifier_rule **cast =
		(const struct classifier_rule **)rules;
	if (kind == CORACLE_LINE) {
		return classify_coracle_line_compile(
			context, name, cast, rule_count, &attr->line, stage
		);
	}
	return classify_coracle_port_compile(
		context, name, cast, rule_count, &attr->port, stage
	);
}

static struct vline *
coracle_attr_line(enum coracle_attr_kind kind, union coracle_attr *attr) {
	return kind == CORACLE_LINE ? &attr->line.line : &attr->port.line;
}

static uint32_t *
coracle_vline_values(struct vline *line) {
	return ADDR_OF(&line->values);
}

static const char *
coracle_vline_context_name(struct vline *line) {
	return ADDR_OF(&line->memory_context)->name;
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
coracle_attr_free(
	enum coracle_attr_kind kind,
	struct memory_context *context,
	union coracle_attr *attr,
	struct classifier *stage,
	uint32_t rule_count
) {
	classifier_fini(stage, context, rule_count);
	vline_free(coracle_attr_line(kind, attr));
}

// Compiles and frees an attribute iterations times and returns the
// nanoseconds taken, or -1 when a compile fails.
static int64_t
coracle_attr_compile_loop(
	enum coracle_attr_kind kind,
	struct memory_context *context,
	const char *name,
	void **rules,
	uint32_t rule_count,
	uint64_t iterations
) {
	struct timespec start;
	struct timespec end;
	clock_gettime(CLOCK_MONOTONIC, &start);
	for (uint64_t idx = 0; idx < iterations; ++idx) {
		union coracle_attr attr;
		struct classifier stage;
		if (coracle_attr_compile(
			    kind, context, name, rules, rule_count, &attr, &stage
		    )) {
			return -1;
		}
		coracle_attr_free(kind, context, &attr, &stage, rule_count);
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

// PortRule is one rule of a port compile: its intervals, or none at all
// for a rule absent from the projection.
type PortRule struct {
	Ranges [][2]uint16
	Absent bool
}

// AttrResult is what a C attribute compile produced, copied out of
// shared memory.
type AttrResult struct {
	Values      []uint32
	RuleGroups  []uint32
	Groups      [][]uint32
	MaxValue    uint32
	ContextName string
}

// CRules is a ruleset in C memory, as one attribute compile reads it.
type CRules struct {
	kind     C.enum_coracle_attr_kind
	pointers *unsafe.Pointer
	count    C.uint32_t
	blocks   []unsafe.Pointer
}

// NewCLineRules copies line rules into C memory; Free releases it.
func NewCLineRules(rules []LineRule) *CRules {
	m := newCRules(C.CORACLE_LINE, len(rules))
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
		pointers[idx] = unsafe.Pointer(view)
	}
	return m
}

// NewCPortRules copies port rules into C memory; Free releases it.
func NewCPortRules(rules []PortRule) *CRules {
	m := newCRules(C.CORACLE_PORT, len(rules))
	pointers := unsafe.Slice(m.pointers, len(rules))
	for idx, rule := range rules {
		if rule.Absent {
			continue
		}
		view := (*C.struct_filter_u16_ranges)(m.alloc(1, unsafe.Sizeof(C.struct_filter_u16_ranges{})))
		if len(rule.Ranges) > 0 {
			items := unsafe.Slice((*C.struct_filter_u16_span)(m.alloc(len(rule.Ranges), unsafe.Sizeof(C.struct_filter_u16_span{}))), len(rule.Ranges))
			for rangeIdx, span := range rule.Ranges {
				items[rangeIdx].from = C.uint16_t(span[0])
				items[rangeIdx].to = C.uint16_t(span[1])
			}
			view.items = &items[0]
		}
		view.count = C.uint32_t(len(rule.Ranges))
		pointers[idx] = unsafe.Pointer(view)
	}
	return m
}

func newCRules(kind C.enum_coracle_attr_kind, count int) *CRules {
	m := &CRules{kind: kind, count: C.uint32_t(count)}
	m.pointers = (*unsafe.Pointer)(m.alloc(max(count, 1), unsafe.Sizeof(uintptr(0))))
	return m
}

func (m *CRules) alloc(count int, size uintptr) unsafe.Pointer {
	block := C.calloc(C.size_t(count), C.size_t(size))
	m.blocks = append(m.blocks, block)
	return block
}

// Free releases the C copy.
func (m *CRules) Free() {
	for _, block := range m.blocks {
		C.free(block)
	}
}

// CompileLoop compiles and frees the attribute of the rules iterations
// times in C, allocating from context, and returns the time the
// compiles took.
func (m *CRules) CompileLoop(context unsafe.Pointer, name string, iterations int) (time.Duration, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	elapsed := C.coracle_attr_compile_loop(m.kind, (*C.struct_memory_context)(context), cName, m.pointers, m.count, C.uint64_t(iterations))
	if elapsed < 0 {
		return 0, fmt.Errorf("the C attribute compile failed")
	}
	return time.Duration(elapsed), nil
}

// Compile runs the C attribute compile of the module SDK over the rules,
// allocating from context, and returns what it produced after freeing
// it again.
func (m *CRules) Compile(context unsafe.Pointer, name string) (*AttrResult, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var attr C.union_coracle_attr
	var stage C.struct_classifier
	memoryContext := (*C.struct_memory_context)(context)
	if C.coracle_attr_compile(m.kind, memoryContext, cName, m.pointers, m.count, &attr, &stage) != 0 {
		return nil, fmt.Errorf("the C attribute compile failed")
	}
	defer C.coracle_attr_free(m.kind, memoryContext, &attr, &stage, m.count)

	line := C.coracle_attr_line(m.kind, &attr)
	result := &AttrResult{
		Values:      append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_vline_values(line))), line.size)...),
		RuleGroups:  append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(stage.rule_groups)), int(m.count))...),
		MaxValue:    uint32(stage.registry.max_value),
		ContextName: C.GoString(C.coracle_vline_context_name(line)),
	}
	for idx := range uint64(stage.registry.range_count) {
		valueRange := C.coracle_stage_range(&stage, C.uint64_t(idx))
		values := unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_range_values(valueRange))), valueRange.count)
		result.Groups = append(result.Groups, append([]uint32{}, values...))
	}
	return result, nil
}
