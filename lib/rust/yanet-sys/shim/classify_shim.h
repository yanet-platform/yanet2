// Helpers over lib/classify, the classification library of the C modules.
//
// The compile side wraps the attribute compiles, the joins and the decode a
// module composes its classifier of, and the frees of what they build; the
// lookup side wraps the batch lookups of the built classifiers. Both are
// compiled into the shim library with hidden visibility, as the dataplane
// shim is, in two objects, so a plugin links only the lookups.
//
// A rule of an attribute compile is the address of that attribute's view of
// the rule (a device set, a network set, a port or line range set, a
// fragment constraint), or NULL for a rule absent from the projection; the
// joins and the decode read only whether a rule is NULL.
#pragma once

#include <stdbool.h>
#include <stdint.h>

#include "lib/classify/rule.h"

#include "shim_visibility.h"

struct classifier;
struct classify_attr_device;
struct classify_attr_ipfrag;
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
yanet_sys_classify_device_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct filter_devices *const *rules,
	uint32_t rule_count,
	struct classify_attr_device *attr,
	struct classifier *stage
);

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

YANET_SYS_HIDDEN int
yanet_sys_classify_ipfrag_compile(
	struct memory_context *memory_context,
	const char *name,
	const enum filter_ip_fragment *const *rules,
	uint32_t rule_count,
	struct classify_attr_ipfrag *attr,
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
yanet_sys_classify_device_free(struct classify_attr_device *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_net4_free(struct classify_attr_net4 *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_net6_free(struct classify_attr_net6 *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_line_free(struct classify_attr_line *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_port_free(struct classify_attr_port *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_ipfrag_free(struct classify_attr_ipfrag *attr);

YANET_SYS_HIDDEN void
yanet_sys_classify_joint_free(struct value_table *joint);

YANET_SYS_HIDDEN void
yanet_sys_classify_rule_table_free(struct rule_table *table);

YANET_SYS_HIDDEN void
yanet_sys_classify_rule_map_free(struct vline *rule_map);

// The batch lookups of the built classifiers: one class or rule per input.
//
// A class or a device outside the table it indexes reads class zero, a rule
// map or a rule table outside it no rule, so a lookup stays inside the
// classifier whatever its input. The attribute lookups read their whole
// domain: the line one a byte, the port one 16 bits, the fragment one whether
// the packet is a fragment.
YANET_SYS_HIDDEN uint32_t
yanet_sys_classify_device_lookup(
	const struct classify_attr_device *attr, uint32_t device
);

// classify_net4_lookup (lib/classify/classifiers/net4.h): addresses as
// their four bytes in network order, one word each.
YANET_SYS_HIDDEN void
yanet_sys_classify_net4_lookup(
	const struct classify_attr_net4 *attr,
	const uint32_t *addrs,
	uint32_t *classes,
	uint32_t count
);

// classify_net6_lookup (lib/classify/classifiers/net6.h): addresses of
// sixteen bytes in network order, one after the other.
YANET_SYS_HIDDEN void
yanet_sys_classify_net6_lookup(
	const struct classify_attr_net6 *attr,
	const uint8_t *addrs,
	uint32_t *classes,
	uint32_t count
);

YANET_SYS_HIDDEN void
yanet_sys_classify_line_lookup(
	const struct classify_attr_line *attr,
	const uint8_t *values,
	uint32_t *classes,
	uint32_t count
);

YANET_SYS_HIDDEN void
yanet_sys_classify_port_lookup(
	const struct classify_attr_port *attr,
	const uint16_t *ports,
	uint32_t *classes,
	uint32_t count
);

YANET_SYS_HIDDEN void
yanet_sys_classify_ipfrag_lookup(
	const struct classify_attr_ipfrag *attr,
	const bool *fragments,
	uint32_t *classes,
	uint32_t count
);

// classify_joint_lookup and classify_joint_lookup_const
// (lib/classify/query.h).
YANET_SYS_HIDDEN void
yanet_sys_classify_joint_lookup(
	const struct value_table *joint,
	const uint32_t *left,
	const uint32_t *right,
	uint32_t *classes,
	uint32_t count
);

YANET_SYS_HIDDEN void
yanet_sys_classify_joint_lookup_const(
	const struct value_table *joint,
	uint32_t left,
	const uint32_t *right,
	uint32_t *classes,
	uint32_t count
);

// classify_rule_table_lookup (lib/classify/query.h).
YANET_SYS_HIDDEN void
yanet_sys_classify_rule_table_lookup(
	const struct rule_table *table,
	const uint32_t *left,
	const uint32_t *right,
	uint32_t *rules,
	uint32_t count
);

// classify_resolve (lib/classify/query.h).
YANET_SYS_HIDDEN void
yanet_sys_classify_resolve(
	const struct vline *rule_map,
	const uint32_t *classes,
	uint32_t *rules,
	uint32_t count
);

// classify_results_min (lib/classify/query.h): keeps the first match of
// two decodings of the same packets.
YANET_SYS_HIDDEN void
yanet_sys_classify_results_min(
	uint32_t *rules, const uint32_t *extra, uint32_t count
);
