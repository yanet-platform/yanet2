#pragma once

#include "../rule.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "dataplane/packet/packet.h"

#include <stdint.h>

#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

static inline int
init_vlan(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	struct value_table *t =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (t == NULL) {
		return -1;
	}
	int res = value_table_init(t, memory_context, 1, 4096);
	if (res < 0) {
		return res;
	}
	SET_OFFSET_OF(data, t);
	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		if (r->vlan_range_count == 0) {
			continue;
		}
		value_table_new_gen(t);
		for (uint32_t idx = 0; idx < r->vlan_range_count; ++idx) {
			for (uint16_t vlan = r->vlan_ranges[idx].from;
			     vlan <= r->vlan_ranges[idx].to;
			     ++vlan) {
				value_table_touch(t, 0, vlan);
			}
		}
	}
	value_table_compact(t);
	for (const struct filter_rule *r = rules; r < rules + rule_count; ++r) {
		value_registry_start(registry);
		if (r->vlan_range_count == 0) {
			for (uint16_t vlan = 0; vlan <= 4095; ++vlan) {
				value_registry_collect(
					registry, value_table_get(t, 0, vlan)
				);
			}
		}
		for (uint32_t idx = 0; idx < r->vlan_range_count; ++idx) {
			for (uint16_t vlan = r->vlan_ranges[idx].from;
			     vlan <= r->vlan_ranges[idx].to;
			     ++vlan) {
				value_registry_collect(
					registry, value_table_get(t, 0, vlan)
				);
			}
		}
	}
	return 0;
}

static inline uint32_t
lookup_vlan(struct packet *packet, void *data) {
	struct value_table *t = (struct value_table *)data;
	uint16_t vlan = rte_be_to_cpu_16(packet->mbuf->vlan_tci);
	return value_table_get(t, 0, vlan);
}

static inline void
free_vlan(void *data, struct memory_context *m) {
	(void)m;
	struct value_table *t = (struct value_table *)data;
	value_table_free(t);
}
