/*
 * Stateful NAT64 module dataplane.
 *
 * The RFC7915 packet translation is the shared core of
 * lib/dataplane/packet/nat64; this module owns the session policy: an
 * IPv6 flow translates to a public IPv4 address picked from the pool, the
 * session is recorded in both fwmaps of the linked nat64state_map object
 * (the lan table keyed by the original IPv6 flow, the wan table keyed by
 * the translated IPv4 flow), and return traffic finds its client through
 * the wan table.
 *
 * Sessions age out through the fwstate layer sweep, exactly like firewall
 * state: untouched entries drain from older layers, touched ones are
 * promoted with their identity preserved.
 */

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

/* DPDK headers */
#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

#include <rte_common.h>
#include <rte_config.h>
#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_log.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "yanet_build_config.h"

/* Project headers */
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/icmp.h"
#include "lib/dataplane/packet/nat64/nat64.h"
#include "lib/dataplane/packet/nat64/nat64_log.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/fwstate/stash_ectx.h"
#include "lib/statemap/fwtable.h"

#include "objects/fwstate/api/fwstate_map_nat64_object.h"

#include "modules/nat64s/dataplane/config.h"
#include "modules/nat64s/dataplane/dataplane.h"

struct nat64s_module {
	struct module module;
};

// 64-bit FNV-1a over the key bytes: picks the pool address of a new
// session deterministically, so every worker derives the same allocation
// for the same flow.
static inline uint64_t
nat64s_key_hash(const void *data, size_t len) {
	const uint8_t *bytes = data;
	uint64_t hash = 0xcbf29ce484222325ull;
	for (size_t i = 0; i < len; ++i) {
		hash ^= bytes[i];
		hash *= 0x100000001b3ull;
	}
	return hash;
}

static inline void
nat64s_stats_bump(uint64_t *stats, enum nat64s_stat stat) {
	__atomic_add_fetch(&stats[stat], 1, __ATOMIC_RELAXED);
}

// The client's transport identity of a lan key: protocol, address and
// source port, without the destination. RFC 6146 5.2 requires the
// external mapping to be endpoint-independent: the same client endpoint
// reaches every destination through the same external address and port.
struct nat64s_client_tuple {
	uint16_t proto;
	uint16_t src_port;
	uint8_t src_addr[16];
};

// The translated port of a new session: its own source port when it
// already lies in the window, else a hash-derived port of the window,
// both keyed by the client tuple alone so every destination of the same
// client endpoint shares one external port.
static inline uint16_t
nat64s_alloc_port(
	const struct nat64s_module_config *config,
	const struct nat64s_client_tuple *client,
	uint16_t sport
) {
	if (sport >= config->port_lo && sport < config->port_hi) {
		return sport;
	}
	return config->port_lo +
	       (uint16_t)(nat64s_key_hash(client, sizeof(*client)) %
			  (uint32_t)(config->port_hi - config->port_lo));
}

// Rewrite one 16-bit transport field and repair its checksum by the
// RFC1624 incremental update. A zero checksum (a UDP datagram over IPv4
// without one) is left zero: nothing verifies it, so there is nothing to
// repair.
static inline void
nat64s_patch_be16(rte_be16_t *field, uint16_t new_host, rte_be16_t *cksum) {
	uint16_t old_host = rte_be_to_cpu_16(*field);
	if (old_host == new_host) {
		return;
	}
	if (*cksum != 0) {
		uint32_t sum = (uint32_t)(~rte_be_to_cpu_16(*cksum) & 0xffff) +
			       (~old_host & 0xffff) + new_host;
		while (sum >> 16) {
			sum = (sum & 0xffff) + (sum >> 16);
		}
		uint16_t hc = (~sum) & 0xffff;
		*cksum = rte_cpu_to_be_16(hc ? hc : 0xffff);
	}
	*field = rte_cpu_to_be_16(new_host);
}

// Byte offsets of the port and checksum fields in a transport header,
// by protocol; the port of an ICMP echo is its identifier.
enum nat64s_field_off {
	NAT64S_OFF_TCP_SRC_PORT = 0,
	NAT64S_OFF_TCP_DST_PORT = 2,
	NAT64S_OFF_TCP_CKSUM = 16,
	NAT64S_OFF_UDP_SRC_PORT = 0,
	NAT64S_OFF_UDP_DST_PORT = 2,
	NAT64S_OFF_UDP_CKSUM = 6,
	NAT64S_OFF_ICMP_ID = 4,
	NAT64S_OFF_ICMP_CKSUM = 2,
};

// The header base, the offset of the requested port field and of the
// checksum, by protocol.
static inline bool
nat64s_transport_fields(
	struct packet *packet,
	uint8_t port_kind, // 0 source, 1 destination
	uint8_t **hdr,
	size_t *port_off,
	size_t *cksum_off
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	*hdr = (uint8_t *)rte_pktmbuf_mtod_offset(
		mbuf, void *, packet->transport_header.offset
	);
	switch (packet->transport_header.type) {
	case IPPROTO_TCP:
		*port_off = (port_kind == 0) ? NAT64S_OFF_TCP_SRC_PORT
					     : NAT64S_OFF_TCP_DST_PORT;
		*cksum_off = NAT64S_OFF_TCP_CKSUM;
		return true;
	case IPPROTO_UDP:
		*port_off = (port_kind == 0) ? NAT64S_OFF_UDP_SRC_PORT
					     : NAT64S_OFF_UDP_DST_PORT;
		*cksum_off = NAT64S_OFF_UDP_CKSUM;
		return true;
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP:
		*port_off = NAT64S_OFF_ICMP_ID;
		*cksum_off = NAT64S_OFF_ICMP_CKSUM;
		return true;
	default:
		return false;
	}
}

// Rewrite the packet's source port (or echo identifier) to the allocated
// port before translation; the shared core then computes the translated
// checksums over the rewritten value.
static inline int
nat64s_patch_source_port(struct packet *packet, uint16_t port) {
	uint8_t *hdr = NULL;
	size_t port_off = 0;
	size_t cksum_off = 0;
	if (!nat64s_transport_fields(packet, 0, &hdr, &port_off, &cksum_off)) {
		return -1;
	}
	nat64s_patch_be16(
		(rte_be16_t *)(hdr + port_off),
		port,
		(rte_be16_t *)(hdr + cksum_off)
	);
	return 0;
}

// Rewrite the packet's destination port (or echo identifier) back to the
// client's original port before translation.
static inline int
nat64s_patch_dest_port(struct packet *packet, uint16_t port) {
	uint8_t *hdr = NULL;
	size_t port_off = 0;
	size_t cksum_off = 0;
	if (!nat64s_transport_fields(packet, 1, &hdr, &port_off, &cksum_off)) {
		return -1;
	}
	nat64s_patch_be16(
		(rte_be16_t *)(hdr + port_off),
		port,
		(rte_be16_t *)(hdr + cksum_off)
	);
	return 0;
}

static inline uint8_t
nat64s_tcp_flags(struct packet *packet) {
	if (packet->transport_header.type != IPPROTO_TCP) {
		return 0;
	}
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, packet->transport_header.offset
	);
	return tcp->tcp_flags;
}

// Whether a client-to-remote packet establishes its TCP session: the
// client's own ACK does. Its SYN alone does not — that is the init.
static inline bool
nat64s_tcp_establishes_lan(struct packet *packet) {
	return (nat64s_tcp_flags(packet) & RTE_TCP_ACK_FLAG) != 0;
}

// Whether a remote-to-client packet establishes the session: the
// remote's SYN answering the client's init — the simultaneous open
// RFC 6146 3.5.2.2 counts as established — or any ACK.
static inline bool
nat64s_tcp_establishes_wan(struct packet *packet) {
	return (nat64s_tcp_flags(packet) & (RTE_TCP_ACK_FLAG | RTE_TCP_SYN_FLAG)
	       ) != 0;
}

// The configured NAT64 prefix a destination embeds under, or NULL when
// it embeds under none: with several prefixes configured, each session
// uses the one its destination actually sits under.
static inline const uint8_t *
nat64s_dst_prefix(
	const struct nat64s_module_config *config, const uint8_t *dst_addr
) {
	uint8_t *prefixes = ADDR_OF(&config->prefixes.list);
	for (uint64_t i = 0; i < config->prefixes.count; ++i) {
		if (memcmp(dst_addr,
			   prefixes + i * NAT64S_PREFIX_LEN,
			   NAT64S_PREFIX_LEN) == 0) {
			return prefixes + i * NAT64S_PREFIX_LEN;
		}
	}
	return NULL;
}

static inline struct nat64_ctx
nat64s_ctx(struct nat64s_module_config *config) {
	return (struct nat64_ctx){
		.mtu_ipv4 = config->mtu.ipv4,
		.mtu_ipv6 = config->mtu.ipv6,
		.malformed = &config->stats[NAT64S_MALFORMED],
	};
}

// The transport identity of a packet: protocol number and the two 16-bit
// ports (source and destination; echo identifiers for ICMP).
//
// Echo messages key on the identifier. Other ICMP messages carry no
// stable flow identity at this layer, so they read whatever the field
// holds: such a session is created on the lan side but never matches its
// reply through the wan table, which drops it.
static inline int
nat64s_transport_of(
	struct packet *packet,
	uint16_t *proto,
	uint16_t *src_port,
	uint16_t *dst_port
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	size_t data_len = rte_pktmbuf_data_len(mbuf);
	uint16_t off = packet->transport_header.offset;
	*proto = packet->transport_header.type;
	switch (*proto) {
	case IPPROTO_TCP: {
		// The parser validates the declared header against the
		// packet's lengths, but a truncated segment still reaches
		// here: check every accessed byte against the first
		// segment's length.
		if (off + offsetof(struct rte_tcp_hdr, dst_port) + 2 >
		    data_len) {
			return -1;
		}
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(tcp->src_port);
		*dst_port = rte_be_to_cpu_16(tcp->dst_port);
		return 0;
	}
	case IPPROTO_UDP: {
		if (off + offsetof(struct rte_udp_hdr, dst_port) + 2 >
		    data_len) {
			return -1;
		}
		struct rte_udp_hdr *udp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(udp->src_port);
		*dst_port = rte_be_to_cpu_16(udp->dst_port);
		return 0;
	}
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP: {
		// The identifier sits four bytes into the ICMP header; a
		// short informational message never reaches that far.
		if (off + NAT64S_OFF_ICMP_ID + sizeof(rte_be16_t) > data_len) {
			return -1;
		}
		struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct yanet_icmp6_hdr *,
			packet->transport_header.offset
		);
		*src_port = rte_be_to_cpu_16(icmp->yanet_icmp6_id);
		*dst_port = *src_port;
		return 0;
	}
	default:
		return -1;
	}
}

// Rewrite one port of the transport header quoted inside an ICMP error,
// before the shared core translates the quote: the receiver must see the
// tuple that existed on its own side of the translator. Returns -1 when
// the quote carries no usable transport header of the given protocol.
static inline int
nat64s_patch_quoted_port(
	struct rte_mbuf *mbuf,
	size_t quote_transport_off,
	uint16_t proto,
	uint8_t port_kind, // 0 source, 1 destination
	uint16_t port
) {
	size_t off = port_kind ? 2 : 0;
	switch (proto) {
	case IPPROTO_TCP:
	case IPPROTO_UDP:
		break;
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP:
		// The identifier doubles as both ports; only a source patch
		// makes sense.
		if (port_kind != 0) {
			return 0;
		}
		off = NAT64S_OFF_ICMP_ID;
		break;
	default:
		return 0;
	}
	size_t field = quote_transport_off + off;
	if (field + 2 > rte_pktmbuf_data_len(mbuf)) {
		return -1;
	}
	rte_be16_t *fieldp = (rte_be16_t *)((uint8_t *)mbuf->buf_addr +
					    mbuf->data_off + field);
	*fieldp = rte_cpu_to_be_16(port);
	return 0;
}

// The session-table protocol of a transport family: both ICMP families
// share one value, so the session an ICMPv6 echo creates is found by its
// translated ICMPv4 reply.
static inline uint16_t
nat64s_session_proto(uint16_t proto) {
	return proto == IPPROTO_ICMPV6 ? IPPROTO_ICMP : proto;
}

// Whether the packet is an ICMP error: an ICMPv6 message below type 128
// or an ICMPv4 Destination Unreachable / Time Exceeded / Parameter
// Problem. Errors carry no flow identity of their own; the session is
// found through the packet they quote.
static inline bool
nat64s_is_icmp_error(struct packet *packet) {
	uint16_t proto = packet->transport_header.type;
	if (proto != IPPROTO_ICMPV6 && proto != IPPROTO_ICMP) {
		return false;
	}
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
		mbuf, struct yanet_icmp6_hdr *, packet->transport_header.offset
	);
	if (proto == IPPROTO_ICMP) {
		return icmp->icmp6_type == ICMP_DEST_UNREACH ||
		       icmp->icmp6_type == ICMP_TIME_EXCEEDED ||
		       icmp->icmp6_type == ICMP_PARAMPROB;
	}
	return icmp->icmp6_type < 128;
}

// The ports of an embedded transport header, by its protocol; the port of
// an ICMP echo is its identifier. Every read is bounds-checked against
// the mbuf's data length.
static inline bool
nat64s_embedded_ports(
	const uint8_t *base,
	size_t off,
	uint16_t proto,
	size_t data_len,
	uint16_t *src_port,
	uint16_t *dst_port
) {
	const rte_be16_t *fields = (const rte_be16_t *)(base + off);
	switch (proto) {
	case IPPROTO_TCP:
	case IPPROTO_UDP:
		if (off + 4 > data_len) {
			return false;
		}
		*src_port = rte_be_to_cpu_16(fields[0]);
		*dst_port = rte_be_to_cpu_16(fields[1]);
		return true;
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP:
		if (off + 6 > data_len) {
			return false;
		}
		// The identifier sits four bytes into the ICMP header.
		rte_be16_t ident = *(const rte_be16_t *)(base + off + 4);
		*src_port = rte_be_to_cpu_16(ident);
		*dst_port = *src_port;
		return true;
	default:
		return false;
	}
}

// The lan side of an ICMPv6 error: find the session of the IPv6 packet
// the error quotes, then rewrite the quoted client address to
// prefix + pool-address so the shared core's embedded translation (which
// derives IPv4 addresses from the address's last four bytes) maps it to
// the session's public address. The outer packet translates with the same
// session's address as its source.
//
// An error can quote either direction of the session — the client's own
// outbound packet, or an inbound one a client-side router could not
// deliver — so the lookup tries the quoted tuple in both orientations.
// Errors never create or refresh sessions: an error about an expired flow
// is dropped, not resurrected.
static int
nat64s_handle_v6_error(
	struct nat64s_module_config *config,
	struct nat64s_prepared *prepared,
	uint64_t now,
	struct packet *packet
) {
	uint64_t *stats = prepared->stats;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	size_t data_len = rte_pktmbuf_data_len(mbuf);
	size_t emb_off =
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr);
	if (emb_off + sizeof(struct rte_ipv6_hdr) > data_len) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}
	struct rte_ipv6_hdr *emb =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, emb_off);

	uint16_t proto = nat64s_session_proto(emb->proto);
	uint16_t sport;
	uint16_t dport;
	if (!nat64s_embedded_ports(
		    (const uint8_t *)mbuf->buf_addr + mbuf->data_off,
		    emb_off + sizeof(struct rte_ipv6_hdr),
		    proto,
		    data_len,
		    &sport,
		    &dport
	    )) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}

	// The client side of the quoted packet: the address that embeds
	// under none of the configured NAT64 prefixes.
	uint8_t *client_addr = emb->dst_addr;
	uint16_t client_port = dport;
	uint16_t remote_port = sport;
	if (nat64s_dst_prefix(config, emb->src_addr) == NULL) {
		client_addr = emb->src_addr;
		client_port = sport;
		remote_port = dport;
	}

	struct nat64state_lan_key key = {
		.hdr =
			{
				.proto = proto,
				.src_port = client_port,
				.dst_port = remote_port,
			},
	};
	memcpy(key.src_addr, client_addr, 16);
	memcpy(key.dst_addr,
	       emb->src_addr == client_addr ? emb->dst_addr : emb->src_addr,
	       16);

	void *found = NULL;
	rwlock_t *lock = NULL;
	bool from_stale = false;
	if (fwtable_lookup(
		    prepared->lan.table, now, &key, &found, &lock, &from_stale
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_WAN_STATE_MISS);
		return -1;
	}
	const struct nat64state_value *session = found;
	uint32_t pool_ip4 = session->ip4;
	uint16_t alloc_port = session->port;
	uint8_t prefix[NAT64S_PREFIX_LEN];
	memcpy(prefix, session->prefix, NAT64S_PREFIX_LEN);
	rwlock_read_unlock(lock);

	// Point the quoted client address at the session's public address,
	// under the session's own prefix, and its port at the allocated
	// port: the quote must show the tuple the remote actually saw.
	memcpy(client_addr, prefix, NAT64S_PREFIX_LEN);
	memcpy(client_addr + NAT64S_PREFIX_LEN, &pool_ip4, sizeof(pool_ip4));
	if (nat64s_patch_quoted_port(
		    mbuf,
		    emb_off + sizeof(struct rte_ipv6_hdr),
		    proto,
		    emb->src_addr == client_addr,
		    alloc_port
	    )) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}

	struct nat64_ctx ctx = nat64s_ctx(config);
	int rc = nat64_translate_v6_to_v4(&ctx, packet, pool_ip4);
	if (rc == 0) {
		nat64s_stats_bump(stats, NAT64S_TRANSLATED_V6_TO_V4);
	}
	return rc;
}

// The wan side of an ICMPv4 error: find the session of the IPv4 packet
// the error quotes. The shared core maps both the quoted pool-side
// address and the outer destination to the client, so passing the
// session's address suffices. The quoted tuple is tried in both
// orientations: an error from the internet quotes the client's outbound
// packet, one generated on the pool side quotes the return traffic.
static int
nat64s_handle_v4_error(
	struct nat64s_module_config *config,
	struct nat64s_prepared *prepared,
	uint64_t now,
	struct packet *packet
) {
	uint64_t *stats = prepared->stats;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	size_t data_len = rte_pktmbuf_data_len(mbuf);
	size_t emb_off =
		packet->transport_header.offset + sizeof(struct rte_icmp_hdr);
	if (emb_off + sizeof(struct rte_ipv4_hdr) > data_len) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}
	struct rte_ipv4_hdr *emb =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv4_hdr *, emb_off);
	size_t emb_ihl = (emb->version_ihl & RTE_IPV4_HDR_IHL_MASK) *
			 RTE_IPV4_IHL_MULTIPLIER;

	uint16_t proto = nat64s_session_proto(emb->next_proto_id);
	uint16_t sport;
	uint16_t dport;
	if (!nat64s_embedded_ports(
		    (const uint8_t *)mbuf->buf_addr + mbuf->data_off,
		    emb_off + emb_ihl,
		    proto,
		    data_len,
		    &sport,
		    &dport
	    )) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}

	uint8_t src6[16];
	for (int orientation = 0; orientation < 2; ++orientation) {
		struct nat64state_wan_key key = {
			.hdr =
				{
					.proto = proto,
					.src_port = orientation ? dport : sport,
					.dst_port = orientation ? sport : dport,
				},
			.local_addr =
				orientation ? emb->dst_addr : emb->src_addr,
			.remote_addr =
				orientation ? emb->src_addr : emb->dst_addr,
		};

		void *found = NULL;
		rwlock_t *lock = NULL;
		bool from_stale = false;
		if (fwtable_lookup(
			    prepared->wan.table,
			    now,
			    &key,
			    &found,
			    &lock,
			    &from_stale
		    ) < 0) {
			continue;
		}
		const struct nat64state_value *session = found;
		uint16_t cli_port = session->cli_port;
		memcpy(src6, session->src6, 16);
		uint8_t prefix[NAT64S_PREFIX_LEN];
		memcpy(prefix, session->prefix, NAT64S_PREFIX_LEN);
		rwlock_read_unlock(lock);

		// The quote shows the outbound tuple as the remote saw it; its
		// source port is the allocated one, which the client must see
		// back as its own.
		if (nat64s_patch_quoted_port(
			    mbuf, emb_off + emb_ihl, proto, 0, cli_port
		    )) {
			nat64s_stats_bump(stats, NAT64S_MALFORMED);
			return -1;
		}

		struct nat64_ctx ctx = nat64s_ctx(config);
		int rc = nat64_translate_v4_to_v6(&ctx, packet, prefix, src6);
		if (rc == 0) {
			nat64s_stats_bump(stats, NAT64S_TRANSLATED_V4_TO_V6);
		}
		return rc;
	}

	nat64s_stats_bump(stats, NAT64S_WAN_STATE_MISS);
	return -1;
}

// The lan side: translate an IPv6 flow to IPv4, creating or refreshing its
// session in both tables. A new session picks its public address from the
// pool by a hash of the lan key; an existing one keeps its allocation (the
// value ops enforce first-writer-wins on refresh). The flow keeps its own
// ports.
static int
nat64s_handle_v6(
	struct nat64s_module_config *config,
	struct nat64s_prepared *prepared,
	uint64_t worker_idx,
	uint64_t now,
	struct packet *packet
) {
	uint64_t *stats = prepared->stats;
	if (prepared->lan.table == NULL || prepared->wan.table == NULL) {
		nat64s_stats_bump(stats, NAT64S_NO_STATE_LINK);
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (nat64s_is_icmp_error(packet)) {
		return nat64s_handle_v6_error(config, prepared, now, packet);
	}

	uint16_t proto;
	uint16_t sport;
	uint16_t dport;
	if (nat64s_transport_of(packet, &proto, &sport, &dport)) {
		LOG_DBG(NAT64, "nat64s: no keyable transport header\n");
		return -1;
	}

	struct nat64state_lan_key key = {
		.hdr = {.proto = nat64s_session_proto(proto),
			.src_port = sport,
			.dst_port = dport},
	};
	rte_memcpy(key.src_addr, ipv6->src_addr, 16);
	rte_memcpy(key.dst_addr, ipv6->dst_addr, 16);

	// RFC 6146 3.5: a source inside a configured prefix is hairpinned
	// traffic, not a client flow — translating it again loops.
	if (nat64s_dst_prefix(config, ipv6->src_addr) != NULL) {
		nat64s_stats_bump(stats, NAT64S_HAIRPIN);
		return -1;
	}

	const uint8_t *prefix = nat64s_dst_prefix(config, ipv6->dst_addr);
	if (prefix == NULL) {
		// The destination embeds under no configured prefix: nothing
		// to translate.
		nat64s_stats_bump(stats, NAT64S_NO_PREFIX);
		return -1;
	}

	struct nat64state_value value = {0};
	rwlock_t *lock = NULL;
	void *found = NULL;
	bool from_stale = false;
	bool established =
		proto == IPPROTO_TCP && nat64s_tcp_establishes_lan(packet);
	bool created = fwtable_lookup(
			       prepared->lan.table,
			       now,
			       &key,
			       &found,
			       &lock,
			       &from_stale
		       ) < 0;
	if (!created) {
		const struct nat64state_value *existing = found;
		value.ip4 = existing->ip4;
		value.port = existing->port;
		// Establishment latches: a session that has carried an ACK
		// stays on the established timeout.
		established = established || existing->tcp_established;
		memcpy(value.prefix, existing->prefix, NAT64S_PREFIX_LEN);
		rwlock_read_unlock(lock);
	} else {
		// RFC 6146 3.5.2.2: IPv6-initiated TCP state is created by a
		// SYN alone; any other first packet would let a spoofed
		// sender fill the table with established-lifetime entries.
		if (proto == IPPROTO_TCP &&
		    !(nat64s_tcp_flags(packet) & RTE_TCP_SYN_FLAG)) {
			nat64s_stats_bump(stats, NAT64S_NOT_SYN);
			return -1;
		}
		uint32_t *pool = ADDR_OF(&config->pool.pool);
		if (config->pool.count == 0) {
			nat64s_stats_bump(stats, NAT64S_POOL_EMPTY);
			return -1;
		}
		struct nat64s_client_tuple client = {
			.proto = key.hdr.proto,
			.src_port = key.hdr.src_port,
		};
		memcpy(client.src_addr, ipv6->src_addr, sizeof(client.src_addr)
		);
		value.ip4 =
			pool[nat64s_key_hash(&client, sizeof(client)) %
			     config->pool.count];
		value.port = nat64s_alloc_port(config, &client, sport);
	}
	value.cli_port = sport;
	memcpy(value.prefix, prefix, NAT64S_PREFIX_LEN);
	value.updated_at = now;
	value.tcp_established = established;
	value.last_ttl =
		nat64state_entry_ttl(proto, established, &config->timeouts);
	uint64_t ttl = (uint64_t)value.last_ttl;

	// Both directions are refreshed on every packet: return traffic only
	// reads the wan entry, so the lan side is what keeps an active flow's
	// reverse mapping alive.
	//
	// The wan entry goes first. Should the lan insert then fail, the
	// orphan wan entry merely ages out and the next packet retries both;
	// the opposite order would leave a lan entry whose flow can never be
	// answered until it expires.
	uint32_t remote_addr = 0;
	memcpy(&remote_addr, &ipv6->dst_addr[12], sizeof(remote_addr));
	struct nat64state_wan_key wan_key = {
		.hdr =
			{
				.proto = nat64s_session_proto(proto),
				.src_port = value.port,
				.dst_port = dport,
			},
		.local_addr = value.ip4,
		.remote_addr = remote_addr,
	};

	// The external tuple is unique per client: when the allocated
	// address and port are already taken towards this remote by
	// another flow — the same in-window source port, or a hash
	// collision — probe neighboring ports of the window before
	// recording anything.
	if (created) {
		void *probe = NULL;
		rwlock_t *probe_lock = NULL;
		bool probe_stale = false;
		uint32_t tries = config->port_hi - config->port_lo;
		if (tries > NAT64S_MAX_PORT_PROBES) {
			tries = NAT64S_MAX_PORT_PROBES;
		}
		while (tries-- > 0 && fwtable_lookup(
					      prepared->wan.table,
					      now,
					      &wan_key,
					      &probe,
					      &probe_lock,
					      &probe_stale
				      ) >= 0) {
			rwlock_read_unlock(probe_lock);
			if (++value.port >= config->port_hi) {
				value.port = config->port_lo;
			}
			wan_key.hdr.src_port = value.port;
		}
	}
	struct nat64state_value wan_value = {0};
	wan_value.updated_at = now;
	wan_value.last_ttl = ttl;
	wan_value.tcp_established = established;
	memcpy(wan_value.prefix, prefix, NAT64S_PREFIX_LEN);
	wan_value.cli_port = sport;
	memcpy(wan_value.src6, ipv6->src_addr, 16);

	if (fwtable_insert(
		    prepared->wan.table,
		    worker_idx,
		    now,
		    ttl,
		    &wan_key,
		    &wan_value,
		    &lock
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	rwlock_write_unlock(lock);

	if (fwtable_insert(
		    prepared->lan.table,
		    worker_idx,
		    now,
		    ttl,
		    &key,
		    &value,
		    &lock
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	rwlock_write_unlock(lock);

	if (value.port != sport &&
	    nat64s_patch_source_port(packet, value.port)) {
		return -1;
	}

	struct nat64_ctx ctx = nat64s_ctx(config);
	int rc = nat64_translate_v6_to_v4(&ctx, packet, value.ip4);
	if (rc == 0) {
		nat64s_stats_bump(stats, NAT64S_TRANSLATED_V6_TO_V4);
	}
	return rc;
}

// The wan side: return traffic finds its client through the wan table,
// refreshes the entry and translates back. A miss is an attack on the pool
// or a reply whose session expired: dropped either way.
static int
nat64s_handle_v4(
	struct nat64s_module_config *config,
	struct nat64s_prepared *prepared,
	uint64_t worker_idx,
	uint64_t now,
	struct packet *packet
) {
	uint64_t *stats = prepared->stats;
	if (prepared->wan.table == NULL) {
		nat64s_stats_bump(stats, NAT64S_NO_STATE_LINK);
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (nat64s_is_icmp_error(packet)) {
		return nat64s_handle_v4_error(config, prepared, now, packet);
	}

	uint16_t proto;
	uint16_t sport;
	uint16_t dport;
	if (nat64s_transport_of(packet, &proto, &sport, &dport)) {
		LOG_DBG(NAT64, "nat64s: no keyable transport header\n");
		return -1;
	}

	struct nat64state_wan_key key = {
		.hdr = {.proto = nat64s_session_proto(proto),
			.src_port = dport,
			.dst_port = sport},
		.local_addr = ipv4->dst_addr,
		.remote_addr = ipv4->src_addr,
	};

	void *found = NULL;
	rwlock_t *lock = NULL;
	bool from_stale = false;
	if (fwtable_lookup(
		    prepared->wan.table, now, &key, &found, &lock, &from_stale
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_WAN_STATE_MISS);
		return -1;
	}
	const struct nat64state_value *session = found;
	uint8_t src6[16];
	uint8_t prefix[NAT64S_PREFIX_LEN];
	uint16_t cli_port = session->cli_port;
	bool established = session->tcp_established;
	rte_memcpy(src6, session->src6, sizeof(src6));
	rte_memcpy(prefix, session->prefix, sizeof(prefix));
	rwlock_read_unlock(lock);

	// Return traffic keeps its own entry alive: a flow whose client is
	// silent for a while must not lose its reverse mapping while the
	// remote still talks.
	struct nat64state_value refresh = {0};
	refresh.updated_at = now;
	refresh.cli_port = cli_port;
	// Establishment latches here too: a return ACK of an establishing
	// handshake moves the session onto the established timeout.
	established = established || nat64s_tcp_establishes_wan(packet);
	refresh.tcp_established = established;
	uint64_t ttl =
		nat64state_entry_ttl(proto, established, &config->timeouts);
	refresh.last_ttl = ttl;
	rte_memcpy(refresh.src6, src6, sizeof(src6));
	if (fwtable_insert(
		    prepared->wan.table,
		    worker_idx,
		    now,
		    ttl,
		    &key,
		    &refresh,
		    &lock
	    ) < 0) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	rwlock_write_unlock(lock);

	if (cli_port != 0 && cli_port != dport &&
	    nat64s_patch_dest_port(packet, cli_port)) {
		return -1;
	}

	struct nat64_ctx ctx = nat64s_ctx(config);
	int rc = nat64_translate_v4_to_v6(&ctx, packet, prefix, src6);
	if (rc == 0) {
		nat64s_stats_bump(stats, NAT64S_TRANSLATED_V4_TO_V6);
	}
	return rc;
}

void
nat64s_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct nat64s_module_config *config = container_of(
		module_ectx->abs_cp_module,
		struct nat64s_module_config,
		cp_module
	);
	struct nat64s_prepared *prepared = module_ectx->abs_module_prepared;
	uint64_t worker_idx = dp_worker->idx;
	uint64_t now = dp_worker->current_time;

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int result = -1;

		// A session keys on the transport identity, so a fragment
		// without one cannot be translated.
		if (packet->fragment_offset != 0 ||
		    (packet->flags & PACKET_FLAG_FRAGMENTED)) {
			nat64s_stats_bump(prepared->stats, NAT64S_FRAGMENTED);
			result = -1;
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			result = nat64s_handle_v4(
				config, prepared, worker_idx, now, packet
			);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			result = nat64s_handle_v6(
				config, prepared, worker_idx, now, packet
			);
		}

		if (result) {
			packet_front_drop(packet_front, packet);
		} else {
			packet_front_output(packet_front, packet);
		}
	}
}

// Resolve the linked map objects into per-worker table links and cache the
// config's stats block.
static void
nat64s_module_commit_ectx(
	struct module_ectx *module_ectx, struct cp_module *cp_module
) {
	struct nat64s_module_config *config =
		container_of(cp_module, struct nat64s_module_config, cp_module);

	struct nat64s_prepared *prepared = module_ectx->abs_module_prepared;
	if (prepared == NULL) {
		return;
	}
	prepared->stats = config->stats;

	struct cp_object *map_object = NULL;
	if (config->map_object_link_idx != NAT64S_OBJECT_LINK_NONE) {
		struct module_object_link_ectx *link = object_link_get_address(
			module_ectx, config->map_object_link_idx
		);
		if (link != NULL) {
			map_object = link->abs_object_ectx->abs_cp_object;
		}
	}

	uint64_t worker_idx = fwstate_stash_worker_idx(module_ectx);
	fwstate_map_nat64_object_link(
		map_object, worker_idx, &prepared->lan, &prepared->wan
	);
}

static void
nat64s_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_nat64s() {
	struct nat64s_module *module =
		(struct nat64s_module *)malloc(sizeof(*module));

	if (module == NULL) {
		RTE_LOG(ERR, NAT64, "nat64s: failed to allocate module\n");
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "nat64s"
	);
	module->module.handler = nat64s_handle_packets;
	module->module.commit_handler = nat64s_module_commit;
	module->module.commit_ectx_handler = nat64s_module_commit_ectx;
	module->module.prepared_size = sizeof(struct nat64s_prepared);

	return &module->module;
}
