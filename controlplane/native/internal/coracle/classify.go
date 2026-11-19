//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdlib.h>
#include <time.h>
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"
#include "lib/classify/compiler/line.h"
#include "lib/classify/compiler/net4.h"
#include "lib/classify/compiler/net6.h"
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

static void
coracle_get_net4s(
	const struct classifier_rule *rule, struct filter_net4s *view
) {
	*view = *(const struct filter_net4s *)rule;
}

static void
coracle_get_net6s(
	const struct classifier_rule *rule, struct filter_net6s *view
) {
	*view = *(const struct filter_net6s *)rule;
}

CLASSIFY_LINE_COMPILE(
	coracle_line, struct classify_attr_line, 0x100, coracle_get_lines
)
CLASSIFY_U16_RANGES_COMPILE(
	coracle_port, struct classify_attr_port, coracle_get_ports
)

enum coracle_attr_kind {
	CORACLE_LINE,
	CORACLE_PORT,
	CORACLE_NET4,
	CORACLE_NET6
};

// One compiled attribute of any kind.
union coracle_attr {
	struct classify_attr_line line;
	struct classify_attr_port port;
	struct classify_attr_net4 net4;
	struct classify_attr_net6 net6;
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
	switch (kind) {
	case CORACLE_LINE:
		return classify_coracle_line_compile(
			context, name, cast, rule_count, &attr->line, stage
		);
	case CORACLE_PORT:
		return classify_coracle_port_compile(
			context, name, cast, rule_count, &attr->port, stage
		);
	case CORACLE_NET6:
		return classify_net6_compile(
			context,
			name,
			cast,
			rule_count,
			coracle_get_net6s,
			&attr->net6,
			stage
		);
	default:
		return classify_net4_compile(
			context,
			name,
			cast,
			rule_count,
			coracle_get_net4s,
			&attr->net4,
			stage
		);
	}
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
	if (kind == CORACLE_NET4) {
		lpm_free(&attr->net4.lpm);
		return;
	}
	if (kind == CORACLE_NET6) {
		classify_attr_net6_free(context, &attr->net6);
		return;
	}
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
	"net/netip"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
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
	// Values holds the committed line of a line or port attribute.
	Values []uint32
	// Stretches holds the lookups of a network attribute's tree, the
	// high half one of an IPv6 attribute.
	Stretches []Stretch
	// LoStretches holds the lookups of the low half tree of an IPv6
	// attribute, and Table its table, row after row.
	LoStretches []Stretch
	Table       [][]uint32
	RuleGroups  []uint32
	Groups      [][]uint32
	MaxValue    uint32
	ContextName string
}

// Stretch is a maximal stretch of keys a prefix tree maps to one value.
type Stretch struct {
	From  string
	To    string
	Value uint32
}

// Stretches returns what a prefix tree maps every key to, as maximal
// stretches of one value in ascending key order, keys of keySize bytes:
// two trees answer every lookup alike exactly when their stretches are
// equal, however their pages are laid out.
func Stretches(tree *layout.Lpm, keySize int) []Stretch {
	var stretches []Stretch
	// The last stretch takes its end only once the next one starts.
	end := make([]byte, keySize)
	lpm.Walk(tree, keySize, func(from, to []byte, value uint32) {
		if count := len(stretches); count == 0 || stretches[count-1].Value != value {
			if count > 0 {
				stretches[count-1].To = string(end)
			}
			stretches = append(stretches, Stretch{From: string(from), Value: value})
		}
		copy(end, to)
	})
	if count := len(stretches); count > 0 {
		stretches[count-1].To = string(end)
	}
	return stretches
}

// Net4Rule is one rule of an IPv4 network compile: its prefixes, or
// none at all for a rule absent from the projection.
type Net4Rule struct {
	Prefixes []netip.Prefix
	Absent   bool
}

// NewCNet4Rules copies IPv4 network rules into C memory; Free releases
// it.
func NewCNet4Rules(rules []Net4Rule) *CRules {
	m := newCRules(C.CORACLE_NET4, len(rules))
	pointers := unsafe.Slice(m.pointers, len(rules))
	for idx, rule := range rules {
		if rule.Absent {
			continue
		}
		view := (*C.struct_filter_net4s)(m.alloc(1, unsafe.Sizeof(C.struct_filter_net4s{})))
		if len(rule.Prefixes) > 0 {
			items := unsafe.Slice((*C.struct_net4)(m.alloc(len(rule.Prefixes), unsafe.Sizeof(C.struct_net4{}))), len(rule.Prefixes))
			for netIdx, prefix := range rule.Prefixes {
				addr := prefix.Addr().As4()
				mask := uint32(0)
				if prefix.Bits() > 0 {
					mask = ^uint32(0) << (32 - prefix.Bits())
				}
				for byteIdx := range 4 {
					items[netIdx].addr[byteIdx] = C.uint8_t(addr[byteIdx])
					items[netIdx].mask[byteIdx] = C.uint8_t(mask >> (24 - 8*byteIdx))
				}
			}
			view.items = &items[0]
		}
		view.count = C.uint32_t(len(rule.Prefixes))
		pointers[idx] = unsafe.Pointer(view)
	}
	return m
}

// Net6Network is an IPv6 network of a rule: an address and a mask.
type Net6Network struct {
	Addr [16]byte
	Mask [16]byte
}

// Net6Rule is one rule of an IPv6 network compile: its networks, or none
// at all for a rule absent from the projection.
type Net6Rule struct {
	Networks []Net6Network
	Absent   bool
}

// NewCNet6Rules copies IPv6 network rules into C memory; Free releases
// it.
func NewCNet6Rules(rules []Net6Rule) *CRules {
	m := newCRules(C.CORACLE_NET6, len(rules))
	pointers := unsafe.Slice(m.pointers, len(rules))
	for idx, rule := range rules {
		if rule.Absent {
			continue
		}
		view := (*C.struct_filter_net6s)(m.alloc(1, unsafe.Sizeof(C.struct_filter_net6s{})))
		if len(rule.Networks) > 0 {
			items := unsafe.Slice((*C.struct_net6)(m.alloc(len(rule.Networks), unsafe.Sizeof(C.struct_net6{}))), len(rule.Networks))
			for netIdx, network := range rule.Networks {
				for byteIdx := range 16 {
					items[netIdx].addr[byteIdx] = C.uint8_t(network.Addr[byteIdx])
					items[netIdx].mask[byteIdx] = C.uint8_t(network.Mask[byteIdx])
				}
			}
			view.items = &items[0]
		}
		view.count = C.uint32_t(len(rule.Networks))
		pointers[idx] = unsafe.Pointer(view)
	}
	return m
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

	result := &AttrResult{
		RuleGroups: append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(stage.rule_groups)), int(m.count))...),
		MaxValue:   uint32(stage.registry.max_value),
	}
	if m.kind == C.CORACLE_NET6 {
		tree := (*layout.ClassifyAttrNet6)(unsafe.Pointer(&attr))
		result.Stretches = Stretches(&tree.Hi, 8)
		result.LoStretches = Stretches(&tree.Lo, 8)
		result.ContextName = shm.String(tree.Hi.MemoryContext.Name[:])
		if values := tree.Comb.Values.Get(); values != nil {
			rows := unsafe.Slice(values, tree.Comb.VDim)
			for row := range rows {
				result.Table = append(result.Table, append([]uint32{}, unsafe.Slice(rows[row].Get(), tree.Comb.HDim)...))
			}
		}
	} else if m.kind == C.CORACLE_NET4 {
		tree := (*layout.ClassifyAttrNet4)(unsafe.Pointer(&attr))
		result.Stretches = Stretches(&tree.Lpm, 4)
		result.ContextName = shm.String(tree.Lpm.MemoryContext.Name[:])
	} else {
		line := C.coracle_attr_line(m.kind, &attr)
		result.Values = append([]uint32{}, unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_vline_values(line))), line.size)...)
		result.ContextName = C.GoString(C.coracle_vline_context_name(line))
	}
	for idx := range uint64(stage.registry.range_count) {
		valueRange := C.coracle_stage_range(&stage, C.uint64_t(idx))
		values := unsafe.Slice((*uint32)(unsafe.Pointer(C.coracle_range_values(valueRange))), valueRange.count)
		result.Groups = append(result.Groups, append([]uint32{}, values...))
	}
	return result, nil
}
