#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_udp.h>

#include "common/memory_address.h"
#include "dataplane/module/module.h"
#include "fwstate/layermap.h"
#include "fwstate/types.h"
#include "lib/dataplane/time/clock.h"
#include "logging/log.h"

#include "config.h"

struct fwstate {
	uint64_t ttl;
	struct fw_state_value value;
};

// Helper function to check if packet is a fw state sync packet
static bool
is_fw_state_sync_packet(
	struct packet *packet, struct fwstate_sync_config *sync_config
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	// Check for multicast Ethernet destination
	struct rte_ether_hdr *eth_hdr =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	if ((eth_hdr->dst_addr.addr_bytes[0] & 1) == 0) {
		return false; // Not multicast
	}

	// Check for VLAN + IPv6 + UDP structure
	if (eth_hdr->ether_type != rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN)) {
		return false;
	}

	struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_vlan_hdr *, sizeof(struct rte_ether_hdr)
	);
	if (vlan_hdr->eth_proto != rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		return false;
	}

	if (packet->transport_header.type != IPPROTO_UDP) {
		return false;
	}

	// Get IPv6 and UDP headers
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv6_hdr *,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr)
	);

	struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_udp_hdr *,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr) +
			sizeof(struct rte_ipv6_hdr)
	);

	// FIXME: support for unicast destination?

	// Check destination port matches configured multicast port
	// (port in config is already big-endian)
	if (udp_hdr->dst_port != sync_config->port_multicast) {
		return false;
	}

	// Check destination IPv6 address matches configured multicast address
	if (memcmp(ipv6_hdr->dst_addr, sync_config->dst_addr_multicast, 16) !=
	    0) {
		return false;
	}

	// Check if UDP payload size is a multiple of fw_state_sync_frame
	uint16_t udp_payload_len = rte_be_to_cpu_16(ipv6_hdr->payload_len) -
				   sizeof(struct rte_udp_hdr);
	if (udp_payload_len % sizeof(struct fw_state_sync_frame) != 0) {
		return false;
	}

	return true;
}

// Build fw_state_value from sync frame
// Uses fib field to determine direction: 0 = forward (INGRESS), 1 = backward
// (EGRESS)
static inline struct fwstate
fwstate_build_value(
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	struct fwstate_timeouts *timeouts_config
) {
	struct fwstate state = {
		.value.external = is_external,
		.value.type = sync_frame->proto,
		.value.flags = sync_frame->flags,
		.value.packets_since_last_sync = 0,
		.value.last_sync = now,
		.value.packets_backward = 0,
		.value.packets_forward = 0,
	};

	// Increment appropriate counter based on fib field
	// fib == 0: forward direction (INGRESS), fib == 1: backward direction
	// (EGRESS)
	if (sync_frame->fib == 0) {
		state.value.packets_forward = 1;
	} else {
		state.value.packets_backward = 1;
	}

	// Determine TTL based on protocol and flags
	// Logic from yanet/dataplane/slow_worker.cpp:496
	state.ttl = timeouts_config->default_;
	if (sync_frame->proto == IPPROTO_UDP) {
		state.ttl = timeouts_config->udp;
	} else if (sync_frame->proto == IPPROTO_TCP) {
		state.ttl = timeouts_config->tcp;
		uint8_t flags =
			state.value.flags.tcp.src | state.value.flags.tcp.dst;
		if (flags & FWSTATE_ACK) {
			state.ttl = timeouts_config->tcp_syn_ack;
		} else if (flags & FWSTATE_SYN) {
			state.ttl = timeouts_config->tcp_syn;
		}
		if (flags & FWSTATE_FIN) {
			state.ttl = timeouts_config->tcp_fin;
		}
	}

	return state;
}

// Process IPv4 state sync frame
static void
fwstate_process_sync_v4(
	fwmap_t *fw4state,
	uint16_t worker_idx,
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	struct fwstate_timeouts *timeouts
) {
	struct fw4_state_key key = {
		.proto = sync_frame->proto,
		.src_port = sync_frame->src_port,
		.dst_port = sync_frame->dst_port,
		._ = 0,
		.src_addr = sync_frame->src_ip,
		.dst_addr = sync_frame->dst_ip,
	};

	// Build proper value structure
	struct fwstate state =
		fwstate_build_value(sync_frame, is_external, now, timeouts);

	// Insert or update the state
	rwlock_t *lock = NULL;
	int64_t result = layermap_put(
		fw4state, worker_idx, now, state.ttl, &key, &state.value, &lock
	);

	if (result < 0) {
		// FIXME: counters
		// FIXME: ratelimit this errors
		LOG(ERROR, "failed to insert IPv4 state: %s", strerror(errno));
	}

	if (lock) {
		rwlock_write_unlock(lock);
	}
}

// Process IPv6 state sync frame
static void
fwstate_process_sync_v6(
	fwmap_t *fw6state,
	uint16_t worker_idx,
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	struct fwstate_timeouts *timeouts
) {
	struct fw6_state_key key = {
		.proto = sync_frame->proto,
		.src_port = sync_frame->src_port,
		.dst_port = sync_frame->dst_port,
	};
	rte_memcpy(key.src_addr, sync_frame->src_ip6, 16);
	rte_memcpy(key.dst_addr, sync_frame->dst_ip6, 16);

	// Build proper value structure
	struct fwstate state =
		fwstate_build_value(sync_frame, is_external, now, timeouts);

	// Insert or update the state
	rwlock_t *lock = NULL;
	int64_t result = layermap_put(
		fw6state, worker_idx, now, state.ttl, &key, &state.value, &lock
	);

	if (result < 0) {
		// FIXME: counters
		// FIXME: ratelimit this errors
		LOG(ERROR, "failed to insert IPv6 state: %s", strerror(errno));
	}

	if (lock) {
		rwlock_write_unlock(lock);
	}
}

void
fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct fwstate_module_config *fwstate_module = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct fwstate_module_config,
		cp_module
	);

	struct fwstate_config *fwstate_config = &fwstate_module->cfg;
	fwmap_t *fw4state = ADDR_OF(&fwstate_config->fw4state);
	fwmap_t *fw6state = ADDR_OF(&fwstate_config->fw6state);

	uint64_t now = tsc_clock_get_time_ns(&dp_worker->clock);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// FIXME: accumulate multiple internal sync frames into one
		// packet before pushing to packet_front->output

		if (!is_fw_state_sync_packet(
			    packet, &fwstate_config->sync_config
		    )) {
			// Not a sync packet, pass through
			packet_front_output(packet_front, packet);
			continue;
		}

		// This is a sync packet - process it
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);

		// Extract sync frames from UDP payload
		const uint16_t vlan_offset = sizeof(struct rte_ether_hdr);
		const uint16_t ipv6_offset =
			vlan_offset + sizeof(struct rte_vlan_hdr);
		const uint16_t udp_offset =
			ipv6_offset + sizeof(struct rte_ipv6_hdr);
		const uint16_t payload_offset =
			udp_offset + sizeof(struct rte_udp_hdr);

		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, ipv6_offset
		);

		// Check if packet is from this machine (internal) or from the
		// network (external)
		bool is_external =
			(memcmp(ipv6_hdr->src_addr, (uint8_t[16]){0}, 16) != 0);

		uint16_t udp_payload_len =
			rte_be_to_cpu_16(ipv6_hdr->payload_len) -
			sizeof(struct rte_udp_hdr);
		size_t frame_count =
			udp_payload_len / sizeof(struct fw_state_sync_frame);

		// Process each sync frame in the packet
		for (size_t idx = 0; idx < frame_count; ++idx) {
			struct fw_state_sync_frame *sync_frame =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct fw_state_sync_frame *,
					payload_offset +
						idx * sizeof(struct
							     fw_state_sync_frame
						      )
				);

			if (sync_frame->addr_type == FW_STATE_ADDR_TYPE_IP4) {
				fwstate_process_sync_v4(
					fw4state,
					(uint16_t)dp_worker->idx,
					sync_frame,
					is_external,
					now,
					&fwstate_config->sync_config.timeouts
				);
			} else if (sync_frame->addr_type ==
				   FW_STATE_ADDR_TYPE_IP6) {
				fwstate_process_sync_v6(
					fw6state,
					(uint16_t)dp_worker->idx,
					sync_frame,
					is_external,
					now,
					&fwstate_config->sync_config.timeouts
				);
			}
		}

		// Drop external packets (from other firewalls) after
		// processing. Pass through internal packets (from our ACL) to
		// reach other firewalls.
		if (is_external) {
			packet_front_drop(packet_front, packet);
		} else {
			rte_memcpy(
				ipv6_hdr->src_addr,
				fwstate_config->sync_config.src_addr,
				16
			);
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf, struct rte_udp_hdr *, udp_offset
			);
			udp_hdr->dgram_cksum = 0;
			udp_hdr->dgram_cksum =
				rte_ipv6_udptcp_cksum(ipv6_hdr, udp_hdr);
			packet_front_output(packet_front, packet);
		}
	}
}

struct fwstate_module {
	struct module module;
};

struct module *
new_module_fwstate() {
	struct fwstate_module *module =
		(struct fwstate_module *)malloc(sizeof(struct fwstate_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		"fwstate"
	);
	module->module.handler = fwstate_handle_packets;

	return &module->module;
}
