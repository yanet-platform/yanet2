#include <stddef.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_udp.h>

#include "yanet_build_config.h" // MBUF_MAX_SIZE

#include "common/memory_address.h"
#include "lib/controlplane/config/econtext.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/time/clock.h"
#include "lib/dataplane/worker/worker.h"
#include "lib/fwstate/stash.h"
#include "lib/fwstate/stash_ectx.h"
#include "lib/fwstate/sync.h"
#include "lib/fwstate/types.h"
#include "lib/logging/log.h"
#include "lib/statemap/fwtable.h"
#include "objects/fwstate/api/fwstate_map_v4_object.h"
#include "objects/fwstate/api/fwstate_map_v6_object.h"

#include "config.h"
#include "dataplane.h"

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

// Report whether a packet is an external sync packet this configuration
// receives.
//
// Only packets matching the configured multicast receive contract
// qualify; without a multicast endpoint nothing is claimed. Packets a
// local fwstate emitted carry the internal flag and are recognized by
// the caller before this check.
static bool
is_fw_state_sync_packet(
	struct packet *packet, struct fwstate_sync_config *sync_config
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (!fwstate_sync_multicast_enabled(sync_config)) {
		return false;
	}

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

	// Port values are stored in network byte order.
	if (udp_hdr->dst_port != sync_config->port_multicast) {
		return false;
	}

	if (memcmp(ipv6_hdr->dst_addr, sync_config->dst_addr_multicast, 16) !=
	    0) {
		return false;
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
	// The flag marks the packet as a local emission, so a downstream
	// fwstate passes it through instead of applying it as external.
	packet->flags |= 1U << PACKET_FLAG_FWSTATE_SYNC_INTERNAL;
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
// Missing storage or a failed insertion still returns true: the stash
// consumer uses false solely to mark a local record suppressed, so it is
// never emitted.
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
		// outcome stays with the caller.
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

// Wire bytes of a sync packet in front of its frames: Ethernet, VLAN,
// IPv6 and UDP headers.
#define FWSTATE_SYNC_HEADERS_LEN                                               \
	(sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr) +          \
	 sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_udp_hdr))

// Most frames one worker mbuf can carry, whatever the sync MTU.
//
// Worker pools hold MBUF_MAX_SIZE-byte elements, the mbuf header and the
// headroom included, so a larger batch could never leave in one packet.
// The builder still clamps each packet to the actual tailroom.
#define FWSTATE_SYNC_BATCH_MAX                                                 \
	((MBUF_MAX_SIZE - sizeof(struct rte_mbuf) - RTE_PKTMBUF_HEADROOM -     \
	  FWSTATE_SYNC_HEADERS_LEN) /                                          \
	 sizeof(struct fw_state_sync_frame))

// Applied frames waiting to be packed into one wire packet.
//
// All frames leave through the same TX device; the packet takes its RX
// device from the last frame added.
struct fwstate_sync_batch {
	uint32_t count;
	uint32_t limit;
	uint16_t rx_device_id;
	uint16_t tx_device_id;
	const struct fw_state_sync_frame *frames[FWSTATE_SYNC_BATCH_MAX];
};

// Emission context of one handler call.
struct fwstate_emitter {
	struct dp_worker *dp_worker;
	struct module_ectx *module_ectx;
	struct packet_front *packet_front;
	const struct fwstate_sync_config *sync_config;
	const struct fwstate_counters *counters;
	bool emit_multicast;
	bool emit_unicast;
	struct fwstate_sync_batch batch;
};

// Stamp one built sync packet for every enabled endpoint and push the
// copies to the output.
//
// With both endpoints the packet is cloned for the unicast copy; a clone
// failure loses only that copy.
static void
fwstate_emit_sync_packet(
	struct fwstate_emitter *emitter, struct packet *packet
) {
	const struct fwstate_sync_config *sync_config = emitter->sync_config;

	struct packet *sync_copy = NULL;
	if (emitter->emit_multicast && emitter->emit_unicast) {
		sync_copy = worker_clone_packet(
			emitter->dp_worker,
			packet,
			emitter->module_ectx->packet_recirc_limit
		);
		if (unlikely(sync_copy == NULL)) {
			// FIXME: ratelimit this errors
			LOG(ERROR, "failed to clone sync packet");
			emitter->counters->sync_alloc_failed[0] += 1;
		}
	}

	const uint8_t *dst_addr = emitter->emit_multicast
					  ? sync_config->dst_addr_multicast
					  : sync_config->dst_addr_unicast;
	uint16_t dst_port = emitter->emit_multicast
				    ? sync_config->port_multicast
				    : sync_config->port_unicast;
	fwstate_finalize_internal_sync(packet, sync_config, dst_addr, dst_port);
	emitter->counters->internal_forwarded[0] += 1;
	emitter->counters->internal_forwarded[1] +=
		packet_to_mbuf(packet)->pkt_len;
	packet_front_output(emitter->packet_front, packet);

	if (sync_copy != NULL) {
		fwstate_finalize_internal_sync(
			sync_copy,
			sync_config,
			sync_config->dst_addr_unicast,
			sync_config->port_unicast
		);
		emitter->counters->internal_forwarded[0] += 1;
		emitter->counters->internal_forwarded[1] +=
			packet_to_mbuf(sync_copy)->pkt_len;
		packet_front_output(emitter->packet_front, sync_copy);
	}
}

// Pack the batched frames into as few wire packets as the mbuf tailroom
// allows and emit them.
//
// An allocation or build failure drops the remaining frames of the
// batch; their state is already decided and stays.
static void
fwstate_sync_batch_flush(struct fwstate_emitter *emitter) {
	struct fwstate_sync_batch *batch = &emitter->batch;

	uint32_t done = 0;
	while (done < batch->count) {
		struct packet *packet = worker_packet_alloc(emitter->dp_worker);
		if (unlikely(packet == NULL)) {
			// FIXME: ratelimit this errors
			LOG(ERROR, "failed to allocate sync packet");
			emitter->counters->sync_alloc_failed[0] += 1;
			break;
		}
		int written = fwstate_build_sync_packet(
			batch->frames + done,
			batch->count - done,
			batch->rx_device_id,
			batch->tx_device_id,
			packet
		);
		if (unlikely(written <= 0)) {
			worker_packet_free(packet);
			LOG(ERROR, "failed to build sync packet");
			break;
		}
		done += (uint32_t)written;
		fwstate_emit_sync_packet(emitter, packet);
	}

	batch->count = 0;
}

// Queue one applied record for emission.
//
// A full batch or a record for another TX device closes the current
// batch first.
static void
fwstate_sync_batch_add(
	struct fwstate_emitter *emitter,
	const struct fwstate_sync_record *record
) {
	struct fwstate_sync_batch *batch = &emitter->batch;

	if (batch->count > 0 && (batch->count >= batch->limit ||
				 batch->tx_device_id != record->tx_device_id)) {
		fwstate_sync_batch_flush(emitter);
	}

	batch->frames[batch->count++] = &record->frame;
	batch->rx_device_id = record->rx_device_id;
	batch->tx_device_id = record->tx_device_id;
}

// Apply one frame of a family to its table, counting into the family's
// counters; see fwstate_process_sync_v4.
typedef bool (*fwstate_process_sync_fn)(
	fwtable_t *table,
	uint16_t worker_idx,
	struct fw_state_sync_frame *sync_frame,
	bool is_external,
	uint64_t now,
	const struct fwstate_sync_config *sync_config,
	uint64_t *inserted_cnt,
	uint64_t *insert_failed_cnt,
	uint64_t *suppressed_cnt
);

// Handle the records one linked map received since this configuration's
// previous read in the round.
//
// A pending record is decided here: applied to the map's table or
// suppressed. Every applied record is queued for emission by every
// configuration that reads it; a suppressed one is never emitted. The
// family's processor and counters are fixed for the whole walk, so each
// caller gets a copy of the loop specialised for one family.
static inline __attribute__((always_inline)) void
fwstate_consume_records(
	struct fwstate_emitter *emitter,
	const struct fwstate_stash_link *stash,
	uint32_t *processed,
	fwtable_t *table,
	uint64_t now,
	fwstate_process_sync_fn process,
	const struct fwstate_family_counters *counters
) {
	uint16_t worker_idx = (uint16_t)emitter->dp_worker->idx;
	bool emit = emitter->emit_multicast || emitter->emit_unicast;

	uint32_t count = stash->slot->count;
	for (uint32_t idx = *processed; idx < count; ++idx) {
		struct fwstate_sync_record *record = &stash->records[idx];

		if (record->status == FWSTATE_SYNC_RECORD_PENDING) {
			bool applied =
				process(table,
					worker_idx,
					&record->frame,
					false,
					now,
					emitter->sync_config,
					counters->inserted,
					counters->insert_failed,
					counters->suppressed);
			record->status =
				applied ? FWSTATE_SYNC_RECORD_APPLIED
					: FWSTATE_SYNC_RECORD_SUPPRESSED;
		}

		if (emit && record->status == FWSTATE_SYNC_RECORD_APPLIED) {
			fwstate_sync_batch_add(emitter, record);
		}
	}
	*processed = count;
}

static void
fwstate_consume_stash(
	struct fwstate_emitter *emitter,
	const struct fwstate_stash_link *stash,
	uint32_t *processed,
	fwtable_t *table,
	bool is_ipv6,
	uint64_t now
) {
	const struct fwstate_family_counters *counters =
		&emitter->counters->family[is_ipv6];
	if (is_ipv6) {
		fwstate_consume_records(
			emitter,
			stash,
			processed,
			table,
			now,
			fwstate_process_sync_v6,
			counters
		);
	} else {
		fwstate_consume_records(
			emitter,
			stash,
			processed,
			table,
			now,
			fwstate_process_sync_v4,
			counters
		);
	}
}

// Resolve the linked map object of one family, or NULL without a link.
static struct cp_object *
fwstate_linked_object(struct module_ectx *module_ectx, uint64_t link_idx) {
	if (link_idx == FWSTATE_OBJECT_LINK_NONE) {
		return NULL;
	}
	struct module_object_link_ectx *link =
		object_link_get_address(module_ectx, link_idx);
	if (link == NULL) {
		return NULL;
	}
	struct object_ectx *oectx = link->abs_object_ectx;
	return oectx->abs_cp_object;
}

// Apply every frame of an external sync packet, then drop the packet.
static void
fwstate_receive_external(
	struct fwstate_emitter *emitter,
	struct packet *packet,
	fwtable_t *fw4table,
	fwtable_t *fw6table,
	uint64_t now
) {
	const struct fwstate_counters *counters = emitter->counters;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	counters->sync_packets[0] += 1;
	counters->sync_packets[1] += mbuf->pkt_len;

	// Extract sync frames from UDP payload
	const uint16_t vlan_offset = sizeof(struct rte_ether_hdr);
	const uint16_t ipv6_offset = vlan_offset + sizeof(struct rte_vlan_hdr);
	const uint16_t udp_offset = ipv6_offset + sizeof(struct rte_ipv6_hdr);
	const uint16_t payload_offset = udp_offset + sizeof(struct rte_udp_hdr);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, ipv6_offset
	);

	uint16_t udp_payload_len;
	if (!fwstate_sync_payload_len(
		    mbuf, ipv6_hdr, payload_offset, &udp_payload_len
	    )) {
		packet_front_output(emitter->packet_front, packet);
		return;
	}
	size_t frame_count =
		udp_payload_len / sizeof(struct fw_state_sync_frame);

	uint16_t worker_idx = (uint16_t)emitter->dp_worker->idx;
	for (size_t idx = 0; idx < frame_count; ++idx) {
		struct fw_state_sync_frame *sync_frame =
			rte_pktmbuf_mtod_offset(
				mbuf,
				struct fw_state_sync_frame *,
				payload_offset +
					idx * sizeof(struct fw_state_sync_frame)
			);

		if (sync_frame->addr_type == FW_STATE_ADDR_TYPE_IP4) {
			fwstate_process_sync_v4(
				fw4table,
				worker_idx,
				sync_frame,
				true,
				now,
				emitter->sync_config,
				counters->family[0].inserted,
				counters->family[0].insert_failed,
				counters->family[0].suppressed
			);
		} else if (sync_frame->addr_type == FW_STATE_ADDR_TYPE_IP6) {
			fwstate_process_sync_v6(
				fw6table,
				worker_idx,
				sync_frame,
				true,
				now,
				emitter->sync_config,
				counters->family[1].inserted,
				counters->family[1].insert_failed,
				counters->family[1].suppressed
			);
		}
	}

	// Received sync packets are consumed after updating state.
	counters->external_dropped[0] += 1;
	counters->external_dropped[1] += mbuf->pkt_len;
	packet_front_drop(emitter->packet_front, packet);
}

void
fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct fwstate_module_config *fwstate_module = container_of(
		module_ectx->abs_cp_module,
		struct fwstate_module_config,
		cp_module
	);

	// Links, counters and the read position were resolved when the
	// context was committed. A family without a link has a NULL table, so
	// its external frames are counted as failed inserts, and an empty
	// stash.
	struct fwstate_prepared *prepared = module_ectx->abs_module_prepared;
	const struct fwstate_counters *counters = &prepared->counters;
	fwtable_t *fw4table = prepared->map[0].table;
	fwtable_t *fw6table = prepared->map[1].table;

	uint64_t now = dp_worker->current_time;

	const struct fwstate_sync_config *sync_config =
		&fwstate_module->sync_config;
	struct fwstate_emitter emitter;
	emitter.dp_worker = dp_worker;
	emitter.module_ectx = module_ectx;
	emitter.packet_front = packet_front;
	emitter.sync_config = sync_config;
	emitter.counters = counters;
	emitter.emit_multicast = fwstate_sync_multicast_enabled(sync_config);
	emitter.emit_unicast = fwstate_sync_unicast_enabled(sync_config);
	emitter.batch.count = 0;
	emitter.batch.limit =
		fwstate_sync_frames_per_packet(sync_config->sync_mtu);
	if (emitter.batch.limit > FWSTATE_SYNC_BATCH_MAX) {
		emitter.batch.limit = FWSTATE_SYNC_BATCH_MAX;
	}

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		// A local fwstate emitted this packet: its frames are already
		// in the map, so it travels on untouched.
		bool is_internal =
			(packet->flags >> PACKET_FLAG_FWSTATE_SYNC_INTERNAL) &
			1;
		if (is_internal || !is_fw_state_sync_packet(
					   packet, &fwstate_module->sync_config
				   )) {
			counters->passthrough[0] += 1;
			counters->passthrough[1] +=
				packet_to_mbuf(packet)->pkt_len;
			packet_front_output(packet_front, packet);
			continue;
		}

		fwstate_receive_external(
			&emitter, packet, fw4table, fw6table, now
		);
	}

	// A context without a read position has no stash links, so only
	// ordinary input is handled.
	if (prepared->position == NULL) {
		return;
	}

	struct fwstate_stash_position *position = prepared->position;
	uint64_t iteration = *dp_worker->iterations;
	if (position->iteration != iteration) {
		position->processed[0] = 0;
		position->processed[1] = 0;
		position->iteration = iteration;
	}

	for (size_t family = 0; family < 2; ++family) {
		const struct fwstate_stash_link *stash =
			&prepared->map[family].stash;
		if (stash->slot == NULL) {
			continue;
		}
		fwstate_stash_slot_sync_round(stash->slot, iteration);
		fwstate_consume_stash(
			&emitter,
			stash,
			&position->processed[family],
			prepared->map[family].table,
			family == 1,
			now
		);
	}

	if (emitter.batch.count > 0) {
		fwstate_sync_batch_flush(&emitter);
	}
}

struct fwstate_module {
	struct module module;
};

// Link an execution context to its worker's stashes in the linked maps,
// to the config's read position for that worker and to the counters of
// its own storage. The position itself is never reset.
static void
fwstate_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	uint64_t worker_idx = fwstate_stash_worker_idx(module_ectx);
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);
	struct fwstate_prepared *prepared = module_ectx->abs_module_prepared;
	if (prepared == NULL) {
		return;
	}

	// Counter addresses of this context's storage. size=2 counters hold
	// [packets, bytes], size=1 counters [packets].
	struct counter_storage *storage = module_ectx->abs_counter_storage;
	struct fwstate_counters *counters = &prepared->counters;
	counters->sync_packets =
		counter_get_address(config->sync_packets_counter_id, storage);
	counters->passthrough =
		counter_get_address(config->passthrough_counter_id, storage);
	counters->family[0].inserted = counter_get_address(
		config->sync_v4_inserted_counter_id, storage
	);
	counters->family[0].insert_failed = counter_get_address(
		config->sync_v4_insert_failed_counter_id, storage
	);
	counters->family[0].suppressed = counter_get_address(
		config->sync_v4_suppressed_counter_id, storage
	);
	counters->family[1].inserted = counter_get_address(
		config->sync_v6_inserted_counter_id, storage
	);
	counters->family[1].insert_failed = counter_get_address(
		config->sync_v6_insert_failed_counter_id, storage
	);
	counters->family[1].suppressed = counter_get_address(
		config->sync_v6_suppressed_counter_id, storage
	);
	counters->external_dropped = counter_get_address(
		config->external_dropped_counter_id, storage
	);
	counters->internal_forwarded = counter_get_address(
		config->internal_forwarded_counter_id, storage
	);
	counters->sync_alloc_failed = counter_get_address(
		config->sync_alloc_failed_counter_id, storage
	);

	// Without the worker's read position nothing may be consumed, so the
	// stashes stay unlinked while the tables still serve received sync.
	struct fwstate_stash_position *positions = ADDR_OF(&config->positions);
	bool has_position =
		positions != NULL && worker_idx < config->worker_count;
	uint64_t stash_worker =
		has_position ? worker_idx : FWSTATE_WORKER_IDX_NONE;

	fwstate_map_v4_object_link(
		fwstate_linked_object(module_ectx, config->v4_object_link_idx),
		stash_worker,
		&prepared->map[0]
	);
	fwstate_map_v6_object_link(
		fwstate_linked_object(module_ectx, config->v6_object_link_idx),
		stash_worker,
		&prepared->map[1]
	);
	prepared->position = has_position ? positions + worker_idx : NULL;
}

static void
fwstate_module_commit(
	struct dp_config *dp_config, struct cp_module *cp_module
) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_fwstate() {
	struct fwstate_module *module =
		(struct fwstate_module *)malloc(sizeof(struct fwstate_module));

	if (module == NULL) {
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name,
		sizeof(module->module.name),
		"%s",
		FWSTATE_MODULE_NAME
	);
	module->module.handler = fwstate_handle_packets;
	module->module.commit_handler = fwstate_module_commit;
	module->module.commit_ectx_handler = fwstate_module_commit_ectx;
	module->module.prepared_size = sizeof(struct fwstate_prepared);

	return &module->module;
}
