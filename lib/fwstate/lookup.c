#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/packet/packet.h"

#include "layermap.h"
#include "lookup.h"
#include "types.h"

/**
 * Extract transport layer ports from a packet.
 */
static inline void
fwstate_extract_ports(
	struct rte_mbuf *mbuf,
	struct packet *packet,
	uint16_t proto,
	uint16_t *src_port,
	uint16_t *dst_port
) {
	if (proto == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(tcp_hdr->src_port);
		*dst_port = rte_be_to_cpu_16(tcp_hdr->dst_port);
	} else if (proto == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(udp_hdr->src_port);
		*dst_port = rte_be_to_cpu_16(udp_hdr->dst_port);
	} else {
		*src_port = 0;
		*dst_port = 0;
	}
}

/**
 * Build IPv4 state key from packet.
 * For reverse packets (checking state), swap src/dst to match initial 5-tuple.
 */
static inline void
fwstate_build_state_key_v4(
	struct rte_mbuf *mbuf, struct packet *packet, struct fw4_state_key *key
) {
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	key->proto = ipv4_hdr->next_proto_id;
	key->_ = 0;
	// Swap src/dst addresses to match initial 5-tuple stored in state
	key->src_addr = ipv4_hdr->dst_addr;
	key->dst_addr = ipv4_hdr->src_addr;

	// Extract and swap src/dst ports
	uint16_t src_port, dst_port;
	fwstate_extract_ports(mbuf, packet, key->proto, &src_port, &dst_port);
	key->src_port = dst_port; // little-endian
	key->dst_port = src_port; // little-endian
}

/**
 * Build IPv6 state key from packet.
 * For reverse packets (checking state), swap src/dst to match initial 5-tuple.
 */
static inline void
fwstate_build_state_key_v6(
	struct rte_mbuf *mbuf, struct packet *packet, struct fw6_state_key *key
) {
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	key->proto = ipv6_hdr->proto;
	// Swap src/dst addresses to match initial 5-tuple stored in state
	rte_memcpy(key->src_addr, ipv6_hdr->dst_addr, 16);
	rte_memcpy(key->dst_addr, ipv6_hdr->src_addr, 16);

	// Extract and swap src/dst ports
	uint16_t src_port, dst_port;
	fwstate_extract_ports(mbuf, packet, key->proto, &src_port, &dst_port);
	key->src_port = dst_port; // little-endian
	key->dst_port = src_port; // little-endian
}

/**
 * Common state lookup logic.
 */
static inline bool
fwstate_lookup_state(
	fwmap_t *fwstate,
	void *key,
	uint64_t now,
	uint64_t *deadline,
	bool *value_from_stale_layer
) {
	struct fw_state_value *value = NULL;
	rwlock_t *lock = NULL;

	int64_t result = layermap_get_value_and_deadline(
		fwstate,
		now,
		key,
		(void **)&value,
		&lock,
		deadline,
		value_from_stale_layer
	);

	bool found = result >= 0;
	if (found && value) {
		// Increment backward packet counter atomically
		// (we're checking state for return traffic)
		__atomic_fetch_add(
			&value->packets_backward, 1, __ATOMIC_RELAXED
		);
	}

	if (lock) {
		rwlock_read_unlock(lock);
	}

	return found;
}

bool
fwstate_check_state(
	fwmap_t *fwstate,
	struct packet *packet,
	uint64_t now,
	enum sync_packet_direction *sync_required
) {
	if (fwstate == NULL) {
		*sync_required = SYNC_NONE;
		return false;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	uint64_t deadline = now;
	bool found = false;
	bool value_from_stale_layer = false;

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct fw4_state_key key;
		fwstate_build_state_key_v4(mbuf, packet, &key);
		found = fwstate_lookup_state(
			fwstate, &key, now, &deadline, &value_from_stale_layer
		);
	} else {
		struct fw6_state_key key;
		fwstate_build_state_key_v6(mbuf, packet, &key);
		found = fwstate_lookup_state(
			fwstate, &key, now, &deadline, &value_from_stale_layer
		);
	}

	if (found) {
		// Sync required if state is from a stale layer
		if (value_from_stale_layer) {
			*sync_required = SYNC_EGRESS;
		} else if (now < deadline &&
			   (deadline - now) < FW_STATE_SYNC_THRESHOLD) {
			*sync_required = SYNC_EGRESS;
		}
	}

	return found;
}
