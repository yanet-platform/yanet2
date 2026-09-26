#pragma once

#include "common/value.h"

#include "lib/classify/classifiers/device.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/classifiers/port.h"

#include "lib/controlplane/config/cp_module.h"
#include "lib/statemap/fwtable.h"

struct counter_value_handle;

#define ACTION_ALLOW 0
#define ACTION_DENY 1
#define ACTION_COUNT 2
#define ACTION_CHECK_STATE 3
#define ACTION_CREATE_STATE 4
#define ACTION_LOG 5

#define ACL_MAX_ACTIONS 8

// Sentinel for "no object link at this slot". object_link_get_address
// returns NULL for any index >= object_link_count, so a config with no
// map link at this slot resolves to a NULL fwtable.
#define ACL_OBJECT_LINK_NONE UINT64_MAX

struct acl_target {
	// FIXME: use dynamic allocation
	uint64_t actions[ACL_MAX_ACTIONS];
	uint64_t action_count;
	uint64_t counter_id;
};

// Per-worker absolutes for the packet hot path, derived once per
// module execution context by the module's execution-context commit
// handler.
//
// The module counter addresses, the per-rule counter handle array and
// the linked state tables; the state tables are NULL for a family
// with no object link, in which case CHECK_STATE finds no state for
// that family.
struct acl_prepared {
	uint64_t *allow_cnt;
	uint64_t *deny_cnt;
	uint64_t *check_pass_cnt;
	uint64_t *check_miss_cnt;
	uint64_t *create_cnt;
	uint64_t *sync_cnt;
	uint64_t *invalid_cnt;
	uint64_t *non_term_cnt;
	uint64_t *no_match_cnt;
	struct counter_value_handle **rules_handles;
	fwtable_t *fw4table;
	fwtable_t *fw6table;
};

// Leaf classifier: the fragment condition alone, no joint.
struct acl_classifier_frag {
	struct classify_attr_ipfrag frag_attr;
};

/*
 * L2 classifier of the rules without networks: the device attribute
 * alone, so a network-less rule matches every packet of its devices
 * regardless of the protocol family.
 */
struct acl_classifier_l2 {
	struct classify_attr_device dev_attr;
};

/*
 * The ports pair classifier is shared by the tcp and the udp paths of
 * a family: both compile it once over the union of their projections
 * and join its classes through their own root joints.
 */
struct acl_classifier_ports {
	struct classify_attr_port src_attr;
	struct classify_attr_port dst_attr;
	struct value_table joint;
};

// Leaf classifiers of the transport specific paths: the TCP flags byte
// and the ICMP message type, plain lines over the byte domains - the
// path selection guarantees the header is present, so the lines carry
// no absent mark.
struct acl_classifier_tcp {
	struct classify_attr_line flags_attr;
	struct value_table flags_joint;
};

struct acl_classifier_icmp {
	struct classify_attr_line type_attr;
};

/*
 * Family core classifier over the union of every projection of the
 * family: four attributes joined in the pairwise order - the network
 * pair first, the device attribute joining their classes, then the
 * protocol attribute closing the root - so the region-heavy network
 * cross happens between the compact sides instead of against a
 * subtree already carrying the small attributes.
 *
 * The core carries no transport specific attribute: every packet
 * enters exactly one protocol path beside the plain filter, and the
 * path implies the transport header the path attribute reads.
 */
struct acl_classifier_core4 {
	struct classify_attr_device dev_attr;
	struct classify_attr_net4 net4_src_attr;
	struct classify_attr_net4 net4_dst_attr;
	struct classify_attr_line ipproto_attr;
	struct value_table nets_joint;
	struct value_table mid_joint;
	struct value_table proto_joint;
};

struct acl_classifier_core6 {
	struct classify_attr_device dev_attr;
	struct classify_attr_net6 net6_src_attr;
	struct classify_attr_net6 net6_dst_attr;
	struct classify_attr_line ipproto_attr;
	struct value_table nets_joint;
	struct value_table mid_joint;
	struct value_table proto_joint;
};

/*
 * Final filters: the l2 decoder over the device classes, the plain
 * family root joint joining the core classes with the fragment suffix
 * classes, and the protocol path root joints joining the core classes
 * with the path suffix classes, each with its own projection decoder.
 */
struct acl_filter_l2 {
	struct vline rule_map;
};

struct acl_filter_ip4 {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip4_tcp {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip4_udp {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip4_icmp {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip6 {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip6_tcp {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip6_udp {
	struct value_table root_joint;
	struct vline rule_map;
};

struct acl_filter_ip6_icmp {
	struct value_table root_joint;
	struct vline rule_map;
};

/*
 * Ownership: the classifiers are built once per config update and
 * embedded by value, the filters hold only their own joints and
 * decoders; nothing is borrowed across the structs. The destroy frees
 * the filters first, then the classifiers - the joins go before the
 * class sources they were built from; every free is safe on a zeroed
 * struct, so a partially built config of a failed update walks the
 * same total destroy.
 */
struct acl_module_config {
	struct cp_module cp_module;

	struct acl_classifier_l2 classifier_l2;
	struct acl_classifier_core4 classifier_core4;
	struct acl_classifier_frag classifier_frag4;
	struct acl_classifier_ports classifier_ports4;
	struct acl_classifier_tcp classifier_tcp4;
	struct acl_classifier_icmp classifier_icmp4;
	struct acl_classifier_core6 classifier_core6;
	struct acl_classifier_frag classifier_frag6;
	struct acl_classifier_ports classifier_ports6;
	struct acl_classifier_tcp classifier_tcp6;
	struct acl_classifier_icmp classifier_icmp6;

	struct acl_filter_l2 filter_l2;
	struct acl_filter_ip4 filter_ip4;
	struct acl_filter_ip4_tcp filter_ip4_tcp;
	struct acl_filter_ip4_udp filter_ip4_udp;
	struct acl_filter_ip4_icmp filter_ip4_icmp;
	struct acl_filter_ip6 filter_ip6;
	struct acl_filter_ip6_tcp filter_ip6_tcp;
	struct acl_filter_ip6_udp filter_ip6_udp;
	struct acl_filter_ip6_icmp filter_ip6_icmp;

	uint64_t target_count;
	struct acl_target *targets;
	// The targets array, as an absolute address for the packet hot
	// path.
	//
	// The commit handler copies it from the relative field above once
	// per published generation; the array is the config's own memory,
	// so the derivation is generation-invariant. It is zero until
	// then.
	struct acl_target *abs_targets;

	// Index of the per-rule "rules" counter registry within
	// cp_module.runtime_counter_registries. Each per-rule counter_id is
	// resolved against this registry's per-worker storage.
	uint64_t rules_registry_idx;

	// Object link indices for the v4 and v6 fwtables, declared by
	// acl_module_config_update via cp_module_link_object and resolved at
	// ectx build time into per-worker object_ectx entries. The
	// ACL_OBJECT_LINK_NONE sentinel marks an absent link, and CHECK_STATE
	// then finds no state for that family.
	uint64_t v4_object_link_idx;
	uint64_t v6_object_link_idx;
	// Metrics
	uint64_t compilation_time_ns;
	uint64_t filter_rule_count_l2;
	uint64_t filter_rule_count_ip4;
	uint64_t filter_rule_count_ip4_tcp;
	uint64_t filter_rule_count_ip4_udp;
	uint64_t filter_rule_count_ip4_icmp;
	uint64_t filter_rule_count_ip6;
	uint64_t filter_rule_count_ip6_tcp;
	uint64_t filter_rule_count_ip6_udp;
	uint64_t filter_rule_count_ip6_icmp;

	// Module-level counters, registered by acl_module_config_init
	uint64_t no_match_counter_id;
	uint64_t action_allow_counter_id;
	uint64_t action_deny_counter_id;
	uint64_t action_check_pass_counter_id;
	uint64_t action_check_miss_counter_id;
	uint64_t action_create_state_counter_id;
	uint64_t action_invalid_counter_id;
	uint64_t action_non_term_counter_id;
	uint64_t sync_sent_counter_id;
};
