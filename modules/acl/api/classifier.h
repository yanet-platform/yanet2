#pragma once

#include "common/container_of.h"

#include "controlplane.h"

#include "filter/compiler/device.h"
#include "filter/compiler/net4.h"
#include "filter/compiler/net6.h"
#include "filter/compiler/port.h"
#include "filter/compiler/proto_range.h"
#include "filter/compiler/vlan.h"

static inline void
acl_rule_get_devices(
	const struct filter_rule *filter_rule, struct filter_devices *devices
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);

	devices->count = acl_rule->devices.count;
	devices->items = acl_rule->devices.items;
}

static inline void
acl_rule_get_vlan_ranges(
	const struct filter_rule *filter_rule,
	struct filter_vlan_ranges *vlan_ranges
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	vlan_ranges->count = acl_rule->vlan_ranges.count;
	vlan_ranges->items = acl_rule->vlan_ranges.items;
}

static inline void
acl_rule_get_src_net4s(
	const struct filter_rule *filter_rule, struct filter_net4s *net
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	net->count = acl_rule->src_net4s.count;
	net->items = acl_rule->src_net4s.items;
}

static inline void
acl_rule_get_dst_net4s(
	const struct filter_rule *filter_rule, struct filter_net4s *net
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	net->count = acl_rule->dst_net4s.count;
	net->items = acl_rule->dst_net4s.items;
}

static inline void
acl_rule_get_src_net6s(
	const struct filter_rule *filter_rule, struct filter_net6s *net
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	net->count = acl_rule->src_net6s.count;
	net->items = acl_rule->src_net6s.items;
}

static inline void
acl_rule_get_dst_net6s(
	const struct filter_rule *filter_rule, struct filter_net6s *net
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	net->count = acl_rule->dst_net6s.count;
	net->items = acl_rule->dst_net6s.items;
}

static inline void
acl_rule_get_proto_ranges(
	const struct filter_rule *filter_rule,
	struct filter_proto_ranges *proto_ranges
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	proto_ranges->count = acl_rule->proto_ranges.count;
	proto_ranges->items = acl_rule->proto_ranges.items;
}

static inline void
acl_rule_get_port_ranges_src(
	const struct filter_rule *filter_rule,
	struct filter_port_ranges *port_ranges
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	port_ranges->count = acl_rule->src_port_ranges.count;
	port_ranges->items = acl_rule->src_port_ranges.items;
}

static inline void
acl_rule_get_port_ranges_dst(
	const struct filter_rule *filter_rule,
	struct filter_port_ranges *port_ranges
) {
	struct acl_rule *acl_rule =
		container_of(filter_rule, struct acl_rule, filter_rule);
	port_ranges->count = acl_rule->dst_port_ranges.count;
	port_ranges->items = acl_rule->dst_port_ranges.items;
}

static const struct filter_compile_attr_device_handlers
	acl_filter_compile_attr_device = {
		.attr_handlers = filter_compile_get_devices,
		.get_devices = acl_rule_get_devices,
};

static const struct filter_compile_attr_vlan_handlers
	acl_filter_compile_attr_vlan = {
		.attr_handlers = filter_compile_attr_vlan_handlers,
		.get_vlan_ranges = acl_rule_get_vlan_ranges,
};

static const struct filter_compile_attr_net4_handlers
	acl_filter_compile_attr_net4_src = {
		.attr_handlers = filter_compile_get_net,
		.get_net4s = acl_rule_get_src_net4s,
};

static const struct filter_compile_attr_net4_handlers
	acl_filter_compile_attr_net4_dst = {
		.attr_handlers = filter_compile_get_net,
		.get_net4s = acl_rule_get_dst_net4s,
};

static const struct filter_compile_attr_net6s_handlers
	acl_filter_compile_attr_net6_src = {
		.attr_handlers = filter_compile_get_net6s,
		.get_net6s = acl_rule_get_src_net6s,
};

static const struct filter_compile_attr_net6s_handlers
	acl_filter_compile_attr_net6_dst = {
		.attr_handlers = filter_compile_get_net6s,
		.get_net6s = acl_rule_get_dst_net6s,
};

static const struct filter_compile_attr_proto_handlers
	acl_filter_compile_attr_proto_range = {
		.attr_handlers = filter_compile_attr_proto_handlers,
		.get_proto_ranges = acl_rule_get_proto_ranges,
};

static const struct filter_compile_attr_port_handlers
	acl_filter_compile_attr_port_src = {
		.attr_handlers = filter_compile_attr_port_handlers,
		.get_port_ranges = acl_rule_get_port_ranges_src,
};

static const struct filter_compile_attr_port_handlers
	acl_filter_compile_attr_port_dst = {
		.attr_handlers = filter_compile_attr_port_handlers,
		.get_port_ranges = acl_rule_get_port_ranges_dst,
};

const struct filter_compile_attr_handlers *filter_vlan[] = {
	&acl_filter_compile_attr_device.attr_handlers,
	&acl_filter_compile_attr_vlan.attr_handlers,
};

const struct filter_compile_attr_handlers *filter_ip4[] = {
	&acl_filter_compile_attr_device.attr_handlers,
	&acl_filter_compile_attr_vlan.attr_handlers,
	&acl_filter_compile_attr_net4_src.attr_handlers,
	&acl_filter_compile_attr_net4_dst.attr_handlers,
	&acl_filter_compile_attr_proto_range.attr_handlers,
};

const struct filter_compile_attr_handlers *filter_ip4_port[] = {
	&acl_filter_compile_attr_device.attr_handlers,
	&acl_filter_compile_attr_vlan.attr_handlers,
	&acl_filter_compile_attr_net4_src.attr_handlers,
	&acl_filter_compile_attr_net4_dst.attr_handlers,
	&acl_filter_compile_attr_proto_range.attr_handlers,
	&acl_filter_compile_attr_port_src.attr_handlers,
	&acl_filter_compile_attr_port_dst.attr_handlers,
};

const struct filter_compile_attr_handlers *filter_ip6[] = {
	&acl_filter_compile_attr_device.attr_handlers,
	&acl_filter_compile_attr_vlan.attr_handlers,
	&acl_filter_compile_attr_net6_src.attr_handlers,
	&acl_filter_compile_attr_net6_dst.attr_handlers,
	&acl_filter_compile_attr_proto_range.attr_handlers,
};

const struct filter_compile_attr_handlers *filter_ip6_port[] = {
	&acl_filter_compile_attr_device.attr_handlers,
	&acl_filter_compile_attr_vlan.attr_handlers,
	&acl_filter_compile_attr_net6_src.attr_handlers,
	&acl_filter_compile_attr_net6_dst.attr_handlers,
	&acl_filter_compile_attr_proto_range.attr_handlers,
	&acl_filter_compile_attr_port_src.attr_handlers,
	&acl_filter_compile_attr_port_dst.attr_handlers,
};
