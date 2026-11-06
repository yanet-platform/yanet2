// Helpers over lib/classify, the classification library of the C modules.
//
// They wrap the attribute compiles, the joins and the decode a module composes
// its classifier of, and the frees of what they build; the Rust lookups read
// the built classifiers. They are compiled into the shim library with hidden
// visibility, as the dataplane shim is.
//
// A rule of an attribute compile is the address of that attribute's view of
// the rule (a network set, a port or line range set), or NULL for a rule
// absent from the projection; the joins and the decode read only whether a
// rule is NULL.
#pragma once

#include <stdint.h>

#include "lib/classify/rule.h"

#include "shim_visibility.h"

struct classifier;
struct classify_attr_line;
struct classify_attr_net4;
struct classify_attr_net6;
struct classify_attr_port;
struct memory_context;
struct rule_table;
struct value_table;
struct vline;

// The rule index of a packet no rule matches, CLASSIFY_RULE_INVALID
// (lib/classify/classify.h).
#define YANET_SYS_CLASSIFY_NO_RULE 0xffffffffU

// The attribute compiles (lib/classify/compiler/): each compiles the classifier
// of one attribute over the rules in place, and fills the stage of its
// classes.
//
// Returns 0, or -1 when the memory of the context or the heap is exhausted;
// the classifier and the stage are then left zeroed with nothing allocated.
// The line compile covers the byte domain, the port compile the 16 bit one.
YANET_SYS_HIDDEN int
yanet_sys_classify_net4_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_net4s *const *rules,
	uint32_t rule_count,
	struct classify_attr_net4 *attr,
	struct classifier *stage
);

YANET_SYS_HIDDEN int
yanet_sys_classify_net6_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_net6s *const *rules,
	uint32_t rule_count,
	struct classify_attr_net6 *attr,
	struct classifier *stage
);

YANET_SYS_HIDDEN int
yanet_sys_classify_line_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classify_line_ranges *const *rules,
	uint32_t rule_count,
	struct classify_attr_line *attr,
	struct classifier *stage
);

YANET_SYS_HIDDEN int
yanet_sys_classify_port_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_port_ranges *const *rules,
	uint32_t rule_count,
	struct classify_attr_port *attr,
	struct classifier *stage
);

// classifier_fini (lib/classify/classify.h): releases a stage of the rule
// count it was compiled or joined over.
YANET_SYS_HIDDEN void
yanet_sys_classify_stage_free(
	struct classifier *stage,
	struct memory_context *memory_context,
	uint32_t rule_count
);

// classify_join, classify_join_rules and classify_decode
// (lib/classify/compiler/helper.h), for rules as above: 0, or -1 when memory
// is exhausted, with nothing of the output left allocated.
YANET_SYS_HIDDEN int
yanet_sys_classify_join(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	uint32_t rule_count,
	struct value_table *joint,
	struct classifier *stage
);

YANET_SYS_HIDDEN int
yanet_sys_classify_join_rules(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *left,
	const struct classifier *right,
	const void *const *rules,
	uint32_t rule_count,
	struct rule_table *table
);

YANET_SYS_HIDDEN int
yanet_sys_classify_decode(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier *stage,
	const void *const *rules,
	uint32_t rule_count,
	struct vline *rule_map
);

// The frees of what the compiles, the joins and the decode built; each is
// sound on a zeroed object.
YANET_SYS_HIDDEN void
yanet_sys_classify_net4_free(struct classify_attr_net4 *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_net6_free(struct classify_attr_net6 *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_line_free(struct classify_attr_line *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_port_free(struct classify_attr_port *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_joint_free(struct value_table *joint);

YANET_SYS_HIDDEN void
yanet_sys_classify_rule_table_free(struct rule_table *table);

YANET_SYS_HIDDEN void
yanet_sys_classify_rule_map_free(struct vline *rule_map);
