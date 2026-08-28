#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_udp.h>

#include "common/memory_address.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/time/clock.h"
#include "lib/dataplane/worker/worker.h"
#include "lib/fwstate/fwtable.h"
#include "lib/fwstate/sync.h"
#include "lib/fwstate/types.h"
#include "lib/logging/log.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

#include "config.h"

struct fwstate {
	uint64_t ttl;
	struct fw_state_value value;
};

// Validate and return the sync-frame payload length from the IPv6 header.
//
// Returns false when the packet must be rejected — the header stack is not
// fully resident (runt), the UDP claimed length is below the UDP header size
// (underflow), or the claimed payload exceeds the bytes actually resident in
// the first mbuf segment (over-claim / truncated).
//
// On success, *out is the claimed UDP payload length and is fully resident.
// Callers must still check that *out is a non-zero multiple of the frame size.
static inline bool
fwstate_sync_payload_len(
	const struct rte_mbuf *mbuf,
	const struct rte_ipv6_hdr *ipv6_hdr,
	uint16_t payload_offset,
	uint16_t *out
) {
	uint16_t avail = rte_pktmbuf_data_len(mbuf);
	if (avail < payload_offset) {
		return false; // runt
	}
	uint16_t usable = avail - payload_offset;
	uint16_t claimed = rte_be_to_cpu_16(ipv6_hdr->payload_len);
	if (claimed < sizeof(struct rte_udp_hdr)) {
		return false; // underflow
	}
	uint16_t claimed_payload =
		claimed - (uint16_t)sizeof(struct rte_udp_hdr);
	if (claimed_payload > usable) {
		return false; // over-claim / truncated
	}
	*out = claimed_payload;
	return true;
}

// Helper function to check if packet is a fw state sync packet
static bool
is_fw_state_sync_packet(
	struct packet *packet, struct fwstate_sync_config *sync_config
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	bool is_internal =
		(packet->flags >> PACKET_FLAG_FWSTATE_SYNC_INTERNAL) & 1;
	// External frames require multicast; internal events are always
	// claimed.
	//
	// This prevents neutral events from entering ordinary processing when
	// no emission endpoint is configured.
	if (!is_internal && !fwstate_sync_multicast_enabled(sync_config)) {
		return false;
	}

	// Locally crafted packets are identified by trusted packet metadata.
	//
	// Wire packets cannot set this flag and still follow the configured
	// multicast receive contract.
	struct rte_ether_hdr *eth_hdr =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	if (!is_internal && (eth_hdr->dst_addr.addr_bytes[0] & 1) == 0) {
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

	if (!is_internal) {
		// Port values are stored in network byte order.
		if (udp_hdr->dst_port != sync_config->port_multicast) {
			return false;
		}

		if (memcmp(ipv6_hdr->dst_addr,
			   sync_config->dst_addr_multicast,
			   16) != 0) {
			return false;
		}
	}

	// Check if UDP payload size is a multiple of fw_state_sync_frame.
	// Computes a bounded length to guard against underflow and over-claim.
	const uint16_t payload_offset =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr) +
		sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_udp_hdr);
	uint16_t udp_payload_len;
	if (!fwstate_sync_payload_len(
		    mbuf, ipv6_hdr, payload_offset, &udp_payload_len
	    )) {
		return false;
	}
	if (udp_payload_len % sizeof(struct fw_state_sync_frame) != 0) {
		return false;
	}

	return true;
}

static inline void
fwstate_finalize_internal_sync(
	struct packet *packet,
	const struct fwstate_sync_config *sync_config,
	const uint8_t dst_addr[16],
	uint16_t dst_port
) {
	fwstate_sync_set_destination(
		packet, &sync_config->dst_ether, dst_addr, dst_port
	);
	const uint16_t ipv6_offset =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr);
	const uint16_t udp_offset = ipv6_offset + sizeof(struct rte_ipv6_hdr);
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, ipv6_offset
	);
	rte_memcpy(ipv6_hdr->src_addr, sync_config->src_addr, 16);
	struct rte_udp_hdr *udp_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_udp_hdr *, udp_offset);
	udp_hdr->dgram_cksum = 0;
	udp_hdr->dgram_cksum = rte_ipv6_udptcp_cksum(ipv6_hdr, udp_hdr);
	packet->flags &= (uint16_t)~(1U << PACKET_FLAG_FWSTATE_SYNC_INTERNAL);
}

// Build fw_state_value from sync frame
// Uses fib field to determine direction: 0 = forward (INGRESS), 1 = backward
// (EGRESS)
//
// The entry TTL is inflated by sync_suppress_timeout so that a refresh skipped
// by suppression still leaves at least the configured timeout of remaining
// life. See fwstate_should_suppress_sync for the matching debounce check.
static inline struct fwstate
fwstate_build_value(
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	const struct fwstate_sync_config *sync_config
) {
	struct fwstate state = {
		.value.external = is_external,
		.value.flags = sync_frame->flags,
		.value.created_at =
			now, // tentative; may be overwritten during map insert
		.value.updated_at = now,
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

	state.ttl = fwstate_entry_ttl(
			    sync_frame->proto,
			    state.value.flags.raw,
			    &sync_config->timeouts
		    ) +
		    sync_config->sync_suppress_timeout;
	fwstate_value_set_last_ttl(&state.value, state.ttl);

	return state;
}

// Decide whether an incoming sync frame should be suppressed.
//
// Returns true when the existing entry is still alive and the new frame only
// marginally extends its expiry deadline — within sync_suppress_timeout of the
// current one — i.e. the refresh carries no useful lifetime change.
//
// A frame whose new deadline does not extend the current one (a shorter TTL,
// e.g. a FIN/RST tearing down an established entry) is never suppressed, so the
// state transition is applied. Likewise a frame that extends the deadline past
// the window (e.g. SYN progressing to established) is applied. Zero suppress
// disables the feature and this helper returns false.
static inline bool
fwstate_should_suppress_sync(
	uint64_t current_deadline,
	uint64_t now,
	uint64_t new_ttl,
	uint64_t suppress_timeout
) {
	if (suppress_timeout == 0) {
		return false;
	}
	uint64_t new_expiry = now + new_ttl;
	if (new_expiry < current_deadline) {
		return false;
	}
	return (new_expiry - current_deadline) < suppress_timeout;
}

// Sync processors return false only when suppression rejects a frame.
//
// Missing storage or a failed insertion still returns true: the packet-level
// caller uses false solely to drop a fully suppressed local event before
// emission.
static bool
fwstate_process_sync_v4(
	fwtable_t *fw4table,
	uint16_t worker_idx,
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	const struct fwstate_sync_config *sync_config,
	uint64_t *inserted_cnt,
	uint64_t *insert_failed_cnt,
	uint64_t *suppressed_cnt
) {
	if (fw4table == NULL) {
		// No linked map object for this family: the frame is counted
		// as a failed insert and dropped, while the packet-level
		// outcome (forward internal, drop external) stays with the
		// caller.
		insert_failed_cnt[0] += 1;
		return true;
	}

	struct fw4_state_key key = {
		.hdr.proto = sync_frame->proto,
		.hdr.src_port = sync_frame->src_port,
		.hdr.dst_port = sync_frame->dst_port,
		.src_addr = sync_frame->src_ip,
		.dst_addr = sync_frame->dst_ip,
	};

	// Build proper value structure
	struct fwstate state =
		fwstate_build_value(sync_frame, is_external, now, sync_config);

	// Sync suppression: probe the live entry without taking a write lock.
	// The read lock is released before the put, so the probe is a hint and
	// a benign TOCTOU window remains (at worst a redundant refresh or a
	// missed suppression, neither of which is a correctness invariant).
	if (sync_config->sync_suppress_timeout > 0) {
		struct fw_state_value *existing = NULL;
		rwlock_t *get_lock = NULL;
		uint64_t current_deadline = 0;
		bool from_stale = false;
		int64_t found = fwtable_lookup_with_deadline(
			fw4table,
			now,
			&key,
			(void **)&existing,
			&get_lock,
			&current_deadline,
			&from_stale
		);
		// Snapshot the stored flags while the read lock is still held:
		// reading existing->flags after the unlock would race a
		// concurrent put rewriting the value. Stale-layer hits carry no
		// lock and are never suppressible, so their flags are not read.
		uint8_t existing_flags = 0;
		if (found >= 0 && !from_stale && existing != NULL) {
			existing_flags = existing->flags.raw;
		}
		if (get_lock != NULL) {
			rwlock_read_unlock(get_lock);
		}
		// Never suppress when the entry lives in a stale layer (it must
		// be promoted), nor when the frame carries flag bits the entry
		// has not yet seen — a handshake/teardown transition must
		// always reach the merge path even at an unchanged deadline.
		if (found >= 0 && !from_stale &&
		    (state.value.flags.raw & ~existing_flags) == 0 &&
		    fwstate_should_suppress_sync(
			    current_deadline,
			    now,
			    state.ttl,
			    sync_config->sync_suppress_timeout
		    )) {
			suppressed_cnt[0] += 1;
			return false;
		}
	}

	// Insert or update the state
	rwlock_t *lock = NULL;
	int64_t result = fwtable_insert(
		fw4table, worker_idx, now, state.ttl, &key, &state.value, &lock
	);

	if (result < 0) {
		// FIXME: ratelimit this errors
		LOG(ERROR, "failed to insert IPv4 state: %s", strerror(errno));
		insert_failed_cnt[0] += 1;
	} else {
		inserted_cnt[0] += 1;
	}

	if (lock) {
		rwlock_write_unlock(lock);
	}
	return true;
}

static bool
fwstate_process_sync_v6(
	fwtable_t *fw6table,
	uint16_t worker_idx,
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	const struct fwstate_sync_config *sync_config,
	uint64_t *inserted_cnt,
	uint64_t *insert_failed_cnt,
	uint64_t *suppressed_cnt
) {
	if (fw6table == NULL) {
		insert_failed_cnt[0] += 1;
		return true;
	}

	struct fw6_state_key key = {
		.hdr.proto = sync_frame->proto,
		.hdr.src_port = sync_frame->src_port,
		.hdr.dst_port = sync_frame->dst_port,
	};
	rte_memcpy(key.src_addr, sync_frame->src_ip6, 16);
	rte_memcpy(key.dst_addr, sync_frame->dst_ip6, 16);

	// Build proper value structure
	struct fwstate state =
		fwstate_build_value(sync_frame, is_external, now, sync_config);

	if (sync_config->sync_suppress_timeout > 0) {
		struct fw_state_value *existing = NULL;
		rwlock_t *get_lock = NULL;
		uint64_t current_deadline = 0;
		bool from_stale = false;
		int64_t found = fwtable_lookup_with_deadline(
			fw6table,
			now,
			&key,
			(void **)&existing,
			&get_lock,
			&current_deadline,
			&from_stale
		);
		// Snapshot the stored flags while the read lock is still held:
		// reading existing->flags after the unlock would race a
		// concurrent put rewriting the value. Stale-layer hits carry no
		// lock and are never suppressible, so their flags are not read.
		uint8_t existing_flags = 0;
		if (found >= 0 && !from_stale && existing != NULL) {
			existing_flags = existing->flags.raw;
		}
		if (get_lock != NULL) {
			rwlock_read_unlock(get_lock);
		}
		// Never suppress when the entry lives in a stale layer (it must
		// be promoted), nor when the frame carries flag bits the entry
		// has not yet seen — a handshake/teardown transition must
		// always reach the merge path even at an unchanged deadline.
		if (found >= 0 && !from_stale &&
		    (state.value.flags.raw & ~existing_flags) == 0 &&
		    fwstate_should_suppress_sync(
			    current_deadline,
			    now,
			    state.ttl,
			    sync_config->sync_suppress_timeout
		    )) {
			suppressed_cnt[0] += 1;
			return false;
		}
	}

	// Insert or update the state
	rwlock_t *lock = NULL;
	int64_t result = fwtable_insert(
		fw6table, worker_idx, now, state.ttl, &key, &state.value, &lock
	);

	if (result < 0) {
		// FIXME: ratelimit this errors
		LOG(ERROR, "failed to insert IPv6 state: %s", strerror(errno));
		insert_failed_cnt[0] += 1;
	} else {
		inserted_cnt[0] += 1;
	}

	if (lock) {
		rwlock_write_unlock(lock);
	}
	return true;
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

	// fwtables of the linked map objects, one per family. NULL when the
	// config declared no link for the family, in which case that
	// family's sync frames are counted as failed inserts and dropped.
	fwtable_t *fw4table = NULL;
	fwtable_t *fw6table = NULL;
	if (fwstate_module->v4_object_link_idx != FWSTATE_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, fwstate_module->v4_object_link_idx
		);
		if (link != NULL) {
			struct object_ectx *oectx = ADDR_OF(&link->object_ectx);
			struct cp_object *cp_obj = ADDR_OF(&oectx->cp_object);
			fw4table = fwstate_map_v4_object_table(cp_obj);
		}
	}
	if (fwstate_module->v6_object_link_idx != FWSTATE_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, fwstate_module->v6_object_link_idx
		);
		if (link != NULL) {
			struct object_ectx *oectx = ADDR_OF(&link->object_ectx);
			struct cp_object *cp_obj = ADDR_OF(&oectx->cp_object);
			fw6table = fwstate_map_v6_object_table(cp_obj);
		}
	}

	uint64_t now = dp_worker->current_time;

	// Resolve per-worker counter addresses.
	// size=2 counters: [0]=packets, [1]=bytes; size=1 counters:
	// [0]=packets.
	struct counter_storage *counter_storage =
		ADDR_OF_NONNULL(&module_ectx->counter_storage);

	uint64_t *sync_packets_cnt = counter_get_address(
		fwstate_module->sync_packets_counter_id, counter_storage
	);
	uint64_t *passthrough_cnt = counter_get_address(
		fwstate_module->passthrough_counter_id, counter_storage
	);
	uint64_t *sync_v4_inserted_cnt = counter_get_address(
		fwstate_module->sync_v4_inserted_counter_id, counter_storage
	);
	uint64_t *sync_v6_inserted_cnt = counter_get_address(
		fwstate_module->sync_v6_inserted_counter_id, counter_storage
	);
	uint64_t *sync_v4_insert_failed_cnt = counter_get_address(
		fwstate_module->sync_v4_insert_failed_counter_id,
		counter_storage
	);
	uint64_t *sync_v6_insert_failed_cnt = counter_get_address(
		fwstate_module->sync_v6_insert_failed_counter_id,
		counter_storage
	);
	uint64_t *sync_v4_suppressed_cnt = counter_get_address(
		fwstate_module->sync_v4_suppressed_counter_id, counter_storage
	);
	uint64_t *sync_v6_suppressed_cnt = counter_get_address(
		fwstate_module->sync_v6_suppressed_counter_id, counter_storage
	);
	uint64_t *external_dropped_cnt = counter_get_address(
		fwstate_module->external_dropped_counter_id, counter_storage
	);
	uint64_t *internal_forwarded_cnt = counter_get_address(
		fwstate_module->internal_forwarded_counter_id, counter_storage
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// FIXME: accumulate multiple internal sync frames into one
		// packet before pushing to packet_front->output

		if (!is_fw_state_sync_packet(
			    packet, &fwstate_module->sync_config
		    )) {
			// Not a sync packet, pass through
			passthrough_cnt[0] += 1;
			passthrough_cnt[1] += packet_to_mbuf(packet)->pkt_len;
			packet_front_output(packet_front, packet);
			continue;
		}

		// This is a sync packet - process it
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);

		sync_packets_cnt[0] += 1;
		sync_packets_cnt[1] += mbuf->pkt_len;

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

		bool is_internal =
			(packet->flags >> PACKET_FLAG_FWSTATE_SYNC_INTERNAL) &
			1;
		bool is_external = !is_internal;

		uint16_t udp_payload_len;
		if (!fwstate_sync_payload_len(
			    mbuf, ipv6_hdr, payload_offset, &udp_payload_len
		    )) {
			packet_front_output(packet_front, packet);
			continue;
		}
		size_t frame_count =
			udp_payload_len / sizeof(struct fw_state_sync_frame);
		bool any_applied = false;

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

			bool applied = false;
			if (sync_frame->addr_type == FW_STATE_ADDR_TYPE_IP4) {
				applied = fwstate_process_sync_v4(
					fw4table,
					(uint16_t)dp_worker->idx,
					sync_frame,
					is_external,
					now,
					&fwstate_module->sync_config,
					sync_v4_inserted_cnt,
					sync_v4_insert_failed_cnt,
					sync_v4_suppressed_cnt
				);
			} else if (sync_frame->addr_type ==
				   FW_STATE_ADDR_TYPE_IP6) {
				applied = fwstate_process_sync_v6(
					fw6table,
					(uint16_t)dp_worker->idx,
					sync_frame,
					is_external,
					now,
					&fwstate_module->sync_config,
					sync_v6_inserted_cnt,
					sync_v6_insert_failed_cnt,
					sync_v6_suppressed_cnt
				);
			}
			any_applied = any_applied || applied;
		}

		// Received sync packets are consumed after updating state.
		// Local events are emitted only when at least one frame was
		// applied.
		if (is_external) {
			external_dropped_cnt[0] += 1;
			external_dropped_cnt[1] += mbuf->pkt_len;
			packet_front_drop(packet_front, packet);
		} else if (any_applied) {
			const struct fwstate_sync_config *sync_config =
				&fwstate_module->sync_config;
			bool emit_multicast =
				fwstate_sync_multicast_enabled(sync_config);
			bool emit_unicast =
				fwstate_sync_unicast_enabled(sync_config);

			if (!emit_multicast && !emit_unicast) {
				packet_front_drop(packet_front, packet);
				continue;
			}

			struct packet *sync_copy = NULL;
			if (emit_multicast && emit_unicast) {
				sync_copy = worker_clone_packet(
					dp_worker,
					packet,
					module_ectx->packet_recirc_limit
				);
				if (unlikely(sync_copy == NULL)) {
					LOG(ERROR,
					    "failed to clone sync packet");
				}
			}

			const uint8_t *dst_addr =
				emit_multicast ? sync_config->dst_addr_multicast
					       : sync_config->dst_addr_unicast;
			uint16_t dst_port =
				emit_multicast ? sync_config->port_multicast
					       : sync_config->port_unicast;
			fwstate_finalize_internal_sync(
				packet, sync_config, dst_addr, dst_port
			);
			internal_forwarded_cnt[0] += 1;
			internal_forwarded_cnt[1] += mbuf->pkt_len;
			packet_front_output(packet_front, packet);

			if (sync_copy != NULL) {
				fwstate_finalize_internal_sync(
					sync_copy,
					sync_config,
					sync_config->dst_addr_unicast,
					sync_config->port_unicast
				);
				internal_forwarded_cnt[0] += 1;
				internal_forwarded_cnt[1] +=
					packet_to_mbuf(sync_copy)->pkt_len;
				packet_front_output(packet_front, sync_copy);
			}
		} else {
			packet_front_drop(packet_front, packet);
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
		FWSTATE_MODULE_NAME
	);
	module->module.handler = fwstate_handle_packets;

	return &module->module;
}
