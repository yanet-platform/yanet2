#pragma once

#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"

#include "lib/controlplane/config/cp_module.h"

#define FORWARD_MODE_NONE 0
#define FORWARD_MODE_IN 1
#define FORWARD_MODE_OUT 2

struct forward_target {
	uint64_t device_id;
	uint64_t counter_id;
	uint8_t mode;
};

// Per-worker anchor of the execution-context commit that binds the
// device mapping into the classifiers.
struct forward_prepared {
	uint64_t anchor;
};

/*
 * Shared core classifier: the device and vlan attributes joined once
 * over the union of the family projections; the family filters and the
 * l2 filter reference its classes.
 *
 * The attributes and the joint are embedded by value and released by
 * the config destroy through the typed attribute frees.
 */
struct fwd_classifier_core {
	struct classify_attr_device dev_attr;
	struct classify_attr_line vlan_attr;
	struct value_table joint; // device classes joined with vlan classes
};

struct fwd_classifier_net4 {
	struct classify_attr_net4 src_attr;
	struct classify_attr_net4 dst_attr;
	struct value_table joint;
};

struct fwd_classifier_net6 {
	struct classify_attr_net6 src_attr;
	struct classify_attr_net6 dst_attr;
	struct value_table joint;
};

/*
 * Final filters: the l2 decoder over the core classes, and the family
 * root joints joining the core classes with the network pair classes,
 * each with its own projection decoder.
 */
struct fwd_filter_vlan {
	struct vline rule_map;
};

struct fwd_filter_ip4 {
	struct value_table root_joint;
	struct vline rule_map;
};

struct fwd_filter_ip6 {
	struct value_table root_joint;
	struct vline rule_map;
};

/*
 * Ownership: the classifiers are built once per config update and
 * embedded by value, the filters hold only their own joints and
 * decoders; nothing is borrowed across the structs, so the value
 * tables, lines and matches keep their internal offset discipline
 * with no wrapper. The destroy frees the filters first, then the
 * classifiers; every free is safe on a zeroed struct, so a partially
 * built config of a failed update walks the same total destroy.
 */
struct forward_module_config {
	struct cp_module cp_module;

	struct fwd_classifier_core classifier_core;
	struct fwd_classifier_net4 classifier_net4;
	struct fwd_classifier_net6 classifier_net6;

	struct fwd_filter_vlan filter_vlan;
	struct fwd_filter_ip4 filter_ip4;
	struct fwd_filter_ip6 filter_ip6;

	uint64_t target_count;
	struct forward_target *targets;
};
