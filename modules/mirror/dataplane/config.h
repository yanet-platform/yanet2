#pragma once

#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"

#include "lib/controlplane/config/cp_module.h"

#define MIRROR_MODE_NONE 0
#define MIRROR_MODE_IN 1
#define MIRROR_MODE_OUT 2

// Per-worker anchor of the execution-context commit that binds the
// device mapping into the classifiers.
struct mirror_prepared {
	uint64_t anchor;
};

struct mirror_target {
	uint64_t device_id;
	uint64_t counter_id;
	uint8_t mode;
};

/*
 * L2 projection classifier: the device and vlan attributes joined
 * into the l2 classes, with the projection decoder folded in.
 *
 * The struct is embedded by value in the config; the attributes, the
 * joint and the decoder die with the config through the typed frees.
 */
struct mirror_classifier_vlan {
	struct classify_attr_device dev_attr;
	struct classify_attr_line vlan_attr;
	struct value_table joint; // device classes joined with vlan classes
	struct vline rule_map;
};

/*
 * IPv4 family projection classifier: four attributes joined in the
 * pairwise order - device with vlan, the network pair, the two partial
 * results into the root - so the region-heavy network cross happens
 * between the compact sides instead of against a subtree already
 * carrying the small attributes, with the projection decoder folded
 * in.
 */
struct mirror_classifier_ip4 {
	struct classify_attr_device dev_attr;
	struct classify_attr_line vlan_attr;
	struct classify_attr_net4 net4_src_attr;
	struct classify_attr_net4 net4_dst_attr;
	struct value_table dev_vlan_joint;
	struct value_table nets_joint;
	struct value_table root_joint;
	struct vline rule_map;
};

// IPv6 family projection classifier, in the same shape as the IPv4
// one.
struct mirror_classifier_ip6 {
	struct classify_attr_device dev_attr;
	struct classify_attr_line vlan_attr;
	struct classify_attr_net6 net6_src_attr;
	struct classify_attr_net6 net6_dst_attr;
	struct value_table dev_vlan_joint;
	struct value_table nets_joint;
	struct value_table root_joint;
	struct vline rule_map;
};

/*
 * Ownership: nothing is shared between the three projections, so each
 * classifier holds its own attributes, joints and decoder embedded by
 * value. The destroy frees each classifier through the typed attribute
 * frees and the table and line frees; every free is safe on a zeroed
 * struct, so a partially built config of a failed update walks the
 * same total destroy.
 */
struct mirror_module_config {
	struct cp_module cp_module;

	struct mirror_classifier_vlan classifier_vlan;
	struct mirror_classifier_ip4 classifier_ip4;
	struct mirror_classifier_ip6 classifier_ip6;

	uint64_t target_count;
	struct mirror_target *targets;
};
