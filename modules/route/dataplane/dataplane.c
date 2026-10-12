#include "config.h"

#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

#include "common/memory.h"
#include "lib/logging/log.h"

#include "lib/controlplane/config/econtext.h"

#include "lib/dataplane/config/zone.h"

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/object/object.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/pipeline/pipeline.h"

struct route_module {
	struct module module;
};

// Why a packet could not be routed.
//
// Meaningful only when a routed packet never reaches the emit: an
// expired TTL is ordinary traceroute traffic, a lookup miss is a
// blackhole, and an empty route list or an unresolved device are
// table-state drops. The TTL verdicts are the module's own; the
// lookup verdicts come from the FIB object's public handler.
enum route_drop_reason {
	ROUTE_DROP_TTL_EXPIRED,
	ROUTE_DROP_NO_ROUTE,
};

static inline void
route_count_packet(
	struct counter_storage *counter_storage,
	uint64_t counter_id,
	struct packet *packet
) {
	uint64_t *counters = counter_get_address(counter_id, counter_storage);
	counters[0] += 1;
	counters[1] += packet_data_len(packet);
}

// Per-family packet preparation: the hop limit check and repair stay
// with the packet, and the key of the FIB's public handler is handed
// out. Returns false on an expired hop limit, the drop the caller
// counts with the family's ttl counter.
static inline bool
route_hop_limit_v4(struct packet *packet, const uint8_t **dst_addr) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	if (header->time_to_live <= 1) {
		return false;
	}
	header->time_to_live -= 1;

	/*
	 * Recalculate IPv4 header checksum using RFC 1624 incremental update.
	 * When TTL is decremented by 1, we add 0xFEFF to the complemented
	 * checksum. This is equivalent to subtracting 0x0100 from the original
	 * checksum (TTL field change).
	 */
	unsigned int sum =
		((~rte_be_to_cpu_16(header->hdr_checksum)) & 0xFFFF) + 0xFEFF;
	header->hdr_checksum = ~rte_cpu_to_be_16(sum + (sum >> 16));

	*dst_addr = (const uint8_t *)&header->dst_addr;
	return true;
}

static inline bool
route_hop_limit_v6(struct packet *packet, const uint8_t **dst_addr) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	if (header->hop_limits <= 1) {
		return false;
	}
	header->hop_limits -= 1;

	*dst_addr = header->dst_addr;
	return true;
}

static void
route_set_packet_destination(struct packet *packet, struct route *route) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	/*
	 * FIXME: should we check that the packet starts from an
	 * ethernet header?
	 */
	struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ether_hdr *, 0);

	LOG_TRACEX(
		,
		"route_set_packet_destination [pre] "
		"src_mac: " RTE_ETHER_ADDR_PRT_FMT
		", dst_mac: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES(&ether_hdr->src_addr),
		RTE_ETHER_ADDR_BYTES(&ether_hdr->dst_addr)
	);

	LOG_TRACEX(
		,
		"route_set_packet_destination [route] "
		"src_mac: " RTE_ETHER_ADDR_PRT_FMT
		", dst_mac: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES((struct rte_ether_addr *)&route->src_addr),
		RTE_ETHER_ADDR_BYTES((struct rte_ether_addr *)&route->dst_addr)
	);

	memcpy(ether_hdr->dst_addr.addr_bytes,
	       route->dst_addr.addr,
	       sizeof(route->dst_addr));

	memcpy(ether_hdr->src_addr.addr_bytes,
	       route->src_addr.addr,
	       sizeof(route->src_addr));

	LOG_TRACEX(
		,
		"route_set_packet_destination [post] "
		"src_mac: " RTE_ETHER_ADDR_PRT_FMT
		", dst_mac: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES(&ether_hdr->src_addr),
		RTE_ETHER_ADDR_BYTES(&ether_hdr->dst_addr)
	);
}

static void
route_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct route_module_config *route_config = container_of(
		module_ectx->abs_cp_module,
		struct route_module_config,
		cp_module
	);

	struct counter_storage *counter_storage =
		module_ectx->abs_counter_storage;

	// The table and its per-nexthop counters sit behind the module's
	// object link, resolved once per batch.
	struct module_object_link_ectx *link = object_link_get_address(
		module_ectx, route_config->fib_link_idx
	);
	if (link == NULL) {
		struct packet *packet;
		while ((packet = packet_list_pop(&packet_front->input)) !=
		       NULL) {
			packet_front_drop(packet_front, packet);
		}
		return;
	}
	struct route_fib_object *fib_object = container_of(
		link->abs_object_ectx->abs_cp_object,
		struct route_fib_object,
		cp_object
	);
	const struct route_fib *fib = &fib_object->fib;
	struct counter_storage *routes_storage =
		ADDR_OF(&link->counter_storage);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		const uint8_t *dst_addr;
		uint32_t addr_len;
		const struct route_family_counter_ids *counters;

		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			counters = &route_config->counters_v4;
			addr_len = 4;
			if (!route_hop_limit_v4(packet, &dst_addr)) {
				route_count_packet(
					counter_storage,
					counters->drop_ttl_expired,
					packet
				);
				packet_front_drop(packet_front, packet);
				continue;
			}
		} else if (
			packet->network_header.type ==
			rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)
		) {
			counters = &route_config->counters_v6;
			addr_len = 16;
			if (!route_hop_limit_v6(packet, &dst_addr)) {
				route_count_packet(
					counter_storage,
					counters->drop_ttl_expired,
					packet
				);
				packet_front_drop(packet_front, packet);
				continue;
			}
		} else {
			route_count_packet(
				counter_storage,
				route_config->drop_non_ip_counter_id,
				packet
			);
			packet_front_drop(packet_front, packet);
			continue;
		}

		// The routing is the FIB object's public handler: the
		// selected nexthop arrives by value, an action the module
		// applies below.
		struct route route;
		switch (route_fib_lookup_route(
			fib, addr_len, dst_addr, packet->hash, &route
		)) {
		case ROUTE_FIB_LOOKUP_MISS:
			route_count_packet(
				counter_storage, counters->drop_no_route, packet
			);
			packet_front_drop(packet_front, packet);
			continue;
		case ROUTE_FIB_LOOKUP_EMPTY:
			route_count_packet(
				counter_storage,
				counters->drop_empty_route_list,
				packet
			);
			packet_front_drop(packet_front, packet);
			continue;
		case ROUTE_FIB_LOOKUP_ROUTE:
			break;
		}

		// An index past the module's device table, or a device absent
		// from this generation, resolves nothing.
		struct module_device_target *device_target =
			module_ectx_device_target(module_ectx, route.device_id);
		if (device_target == NULL) {
			route_count_packet(
				counter_storage,
				counters->drop_device_unresolved,
				packet
			);
			packet_front_drop(packet_front, packet);
			continue;
		}

		route_set_packet_destination(packet, &route);
		packet->tx_device_id = device_target->device_id;
		route_count_packet(
			counter_storage, counters->forwarded, packet
		);
		if (route.counter_id != COUNTER_INVALID) {
			route_count_packet(
				routes_storage, route.counter_id, packet
			);
		}
		module_ectx_route_output(
			module_ectx,
			packet_front,
			device_target->abs_output_entry,
			packet
		);
	}
}

static void
route_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
}

// The object factory shares the module factory's translation unit.
//
// An executable that loads the module links only its dataplane library
// and pulls this unit for the module factory, so the object factory rides
// along instead of needing a dependency of its own.
struct object *
new_object_route_fib() {
	struct object *object =
		(struct object *)calloc(1, sizeof(struct object));
	if (object == NULL) {
		return NULL;
	}

	snprintf(
		object->name, sizeof(object->name), "%s", ROUTE_FIB_OBJECT_TYPE
	);

	return object;
}

struct module *
new_module_route() {
	struct route_module *module =
		(struct route_module *)malloc(sizeof(struct route_module));

	if (module == NULL) {
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "route"
	);
	module->module.handler = route_handle_packets;
	module->module.commit_handler = route_module_commit;

	return &module->module;
}
