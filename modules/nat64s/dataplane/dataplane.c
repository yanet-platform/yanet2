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

// Release the bucket lock a table lookup returned: a hit in a layer past
// the second carries none.
static inline void
nat64s_lookup_unlock(rwlock_t *lock) {
	if (lock != NULL) {
		rwlock_read_unlock(lock);
	}
}

// Release the bucket lock an insert returned. One that found no room
// leaves its bucket write-locked just the same, so the release is not
// optional: a bucket kept locked hangs every later packet that hashes to
// it. The pointer is cleared before every insert, as one that failed
// before taking its lock leaves whatever it was given untouched.
static inline void
nat64s_insert_unlock(rwlock_t *lock) {
	if (lock != NULL) {
		rwlock_write_unlock(lock);
	}
}

// The client's transport identity of a lan key: protocol, address and
// source port, without the destination.
//
// The external address and port of a new session derive from it alone,
// so one client endpoint starts from the same external identity towards
// every destination. That identity is reserved per session, not per
// endpoint: a port of it taken towards one remote moves that session to a
// neighbouring port, while the endpoint's sessions towards other remotes
// keep the derived one.
struct nat64s_client_tuple {
	uint16_t proto;
	uint16_t src_port;
	uint8_t src_addr[16];
};

// The translated port of a new session: its own source port when it
// already lies in the window, else a hash-derived port of the window,
// both keyed by the client tuple alone, so every destination of the same
// client endpoint starts from one external port.
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

// Which port of a transport header is addressed: its source or its
// destination.
enum nat64s_port_kind {
	NAT64S_PORT_SOURCE = 0,
	NAT64S_PORT_DEST = 1,
};

// Whether the first bytes of the transport header lie in the mbuf's first
// segment, where every transport field this module touches is read.
static inline bool
nat64s_transport_fits(struct packet *packet, size_t len) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	return (size_t)packet->transport_header.offset + len <=
	       rte_pktmbuf_data_len(mbuf);
}

// The header base, the offset of the requested port field and of the
// checksum, by protocol; false when the protocol carries none or a field
// lies outside the first segment.
static inline bool
nat64s_transport_fields(
	struct packet *packet,
	enum nat64s_port_kind port_kind,
	uint8_t **hdr,
	size_t *port_off,
	size_t *cksum_off
) {
	switch (packet->transport_header.type) {
	case IPPROTO_TCP:
		*port_off = (port_kind == NAT64S_PORT_SOURCE)
				    ? NAT64S_OFF_TCP_SRC_PORT
				    : NAT64S_OFF_TCP_DST_PORT;
		*cksum_off = NAT64S_OFF_TCP_CKSUM;
		break;
	case IPPROTO_UDP:
		*port_off = (port_kind == NAT64S_PORT_SOURCE)
				    ? NAT64S_OFF_UDP_SRC_PORT
				    : NAT64S_OFF_UDP_DST_PORT;
		*cksum_off = NAT64S_OFF_UDP_CKSUM;
		break;
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP:
		*port_off = NAT64S_OFF_ICMP_ID;
		*cksum_off = NAT64S_OFF_ICMP_CKSUM;
		break;
	default:
		return false;
	}
	size_t last = *port_off > *cksum_off ? *port_off : *cksum_off;
	if (!nat64s_transport_fits(packet, last + sizeof(rte_be16_t))) {
		return false;
	}
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	*hdr = (uint8_t *)rte_pktmbuf_mtod_offset(
		mbuf, void *, packet->transport_header.offset
	);
	return true;
}

// Rewrite the packet's source port (or echo identifier) to the allocated
// port before translation; the shared core then computes the translated
// checksums over the rewritten value. A header too short for the port and
// its checksum is refused untouched.
static inline int
nat64s_patch_source_port(struct packet *packet, uint16_t port) {
	uint8_t *hdr = NULL;
	size_t port_off = 0;
	size_t cksum_off = 0;
	if (!nat64s_transport_fields(
		    packet, NAT64S_PORT_SOURCE, &hdr, &port_off, &cksum_off
	    )) {
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
	if (!nat64s_transport_fields(
		    packet, NAT64S_PORT_DEST, &hdr, &port_off, &cksum_off
	    )) {
		return -1;
	}
	nat64s_patch_be16(
		(rte_be16_t *)(hdr + port_off),
		port,
		(rte_be16_t *)(hdr + cksum_off)
	);
	return 0;
}

// The TCP flags of the packet; a header cut short of its flag byte reads
// as carrying none.
static inline uint8_t
nat64s_tcp_flags(struct packet *packet) {
	if (packet->transport_header.type != IPPROTO_TCP) {
		return 0;
	}
	if (!nat64s_transport_fits(
		    packet, offsetof(struct rte_tcp_hdr, tcp_flags) + 1
	    )) {
		return 0;
	}
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, packet->transport_header.offset
	);
	return tcp->tcp_flags;
}

// Whether a remote-to-client packet establishes the session: the
// remote's SYN answering the client's init, with or without its ACK —
// the simultaneous open RFC 6146 3.5.2.2 counts as established.
//
// Nothing else does (RFC 6146 3.5.2.2): a session the remote has not
// answered ages on the general timeout, whatever else either side sends,
// so a half-open session never lives the established lifetime.
static inline bool
nat64s_tcp_establishes_wan(struct packet *packet) {
	return (nat64s_tcp_flags(packet) & RTE_TCP_SYN_FLAG) != 0;
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

// The Well-Known Prefix of RFC 6052, 64:ff9b::/96, in network order.
static const uint8_t nat64s_well_known_prefix[NAT64S_PREFIX_LEN] = {
	0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0
};

// An IPv4 address in network order, from its octets.
#define NAT64S_IPV4(a, b, c, d)                                                \
	RTE_BE32(                                                              \
		((uint32_t)(a) & 0xff) << 24 | ((uint32_t)(b) & 0xff) << 16 |  \
		((uint32_t)(c) & 0xff) << 8 | ((uint32_t)(d) & 0xff)           \
	)

// The IPv4 blocks no global traffic uses, network order.
//
// Those are the blocks RFC 5735 reserves without global reachability,
// and the shared address space of RFC 6598. Two addresses of the IETF
// protocol assignments block are global all the same and listed apart.
static const struct nat64s_ipv4_range {
	rte_be32_t network;
	rte_be32_t mask;
} nat64s_non_global[] = {
	{NAT64S_IPV4(0, 0, 0, 0), NAT64S_IPV4(255, 0, 0, 0)},
	{NAT64S_IPV4(10, 0, 0, 0), NAT64S_IPV4(255, 0, 0, 0)},
	{NAT64S_IPV4(100, 64, 0, 0), NAT64S_IPV4(255, 192, 0, 0)},
	{NAT64S_IPV4(127, 0, 0, 0), NAT64S_IPV4(255, 0, 0, 0)},
	{NAT64S_IPV4(169, 254, 0, 0), NAT64S_IPV4(255, 255, 0, 0)},
	{NAT64S_IPV4(172, 16, 0, 0), NAT64S_IPV4(255, 240, 0, 0)},
	{NAT64S_IPV4(192, 0, 0, 0), NAT64S_IPV4(255, 255, 255, 0)},
	{NAT64S_IPV4(192, 0, 2, 0), NAT64S_IPV4(255, 255, 255, 0)},
	{NAT64S_IPV4(192, 88, 99, 0), NAT64S_IPV4(255, 255, 255, 0)},
	{NAT64S_IPV4(192, 168, 0, 0), NAT64S_IPV4(255, 255, 0, 0)},
	{NAT64S_IPV4(198, 18, 0, 0), NAT64S_IPV4(255, 254, 0, 0)},
	{NAT64S_IPV4(198, 51, 100, 0), NAT64S_IPV4(255, 255, 255, 0)},
	{NAT64S_IPV4(203, 0, 113, 0), NAT64S_IPV4(255, 255, 255, 0)},
	{NAT64S_IPV4(224, 0, 0, 0), NAT64S_IPV4(240, 0, 0, 0)},
	{NAT64S_IPV4(240, 0, 0, 0), NAT64S_IPV4(240, 0, 0, 0)},
};

// The anycast addresses the IETF protocol assignments block holds for the
// whole Internet.
static const rte_be32_t nat64s_global_anycast[] = {
	NAT64S_IPV4(192, 0, 0, 9),
	NAT64S_IPV4(192, 0, 0, 10),
};

// Whether an address is the Well-Known Prefix around an IPv4 address no
// global traffic uses.
//
// That prefix stands for global addresses only (RFC 6052 3.1), whatever
// the configuration carries, so such a source or destination is refused.
// A network-specific prefix may stand for any address.
static inline bool
nat64s_wkp_hides_non_global(const uint8_t addr[16]) {
	if (memcmp(addr, nat64s_well_known_prefix, NAT64S_PREFIX_LEN) != 0) {
		return false;
	}
	rte_be32_t ipv4;
	memcpy(&ipv4, addr + NAT64S_PREFIX_LEN, sizeof(ipv4));
	for (size_t i = 0;
	     i < sizeof(nat64s_non_global) / sizeof(nat64s_non_global[0]);
	     ++i) {
		if ((ipv4 & nat64s_non_global[i].mask) !=
		    nat64s_non_global[i].network) {
			continue;
		}
		for (size_t j = 0; j < sizeof(nat64s_global_anycast) /
					       sizeof(nat64s_global_anycast[0]);
		     ++j) {
			if (ipv4 == nat64s_global_anycast[j]) {
				return false;
			}
		}
		return true;
	}
	return false;
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
// The whole fixed header has to lie in the first segment, or -1 is
// returned: the translation reads and rewrites the header's checksum
// there, so a header that continues in a later segment is refused, its
// ports present or not. Of the ICMP messages only echoes reach this far:
// every other one is dropped beforehand, as the translation could not
// carry it.
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
		if (off + sizeof(struct rte_tcp_hdr) > data_len) {
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
		if (off + sizeof(struct rte_udp_hdr) > data_len) {
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
		if (off + sizeof(struct rte_icmp_hdr) > data_len) {
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
// tuple that existed on its own side of the translator. The identifier of
// an echo serves as either port. A protocol that carries no port is left
// untouched; -1 is returned when the port lies outside the first
// segment.
static inline int
nat64s_patch_quoted_port(
	struct rte_mbuf *mbuf,
	size_t quote_transport_off,
	uint16_t proto,
	enum nat64s_port_kind port_kind,
	uint16_t port
) {
	size_t off = port_kind == NAT64S_PORT_DEST ? 2 : 0;
	switch (proto) {
	case IPPROTO_TCP:
	case IPPROTO_UDP:
		break;
	case IPPROTO_ICMPV6:
	case IPPROTO_ICMP:
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

// Whether the protocol is an ICMP family: an echo keys on its identifier
// and has no remote port of its own.
static inline bool
nat64s_is_icmp(uint16_t proto) {
	return proto == IPPROTO_ICMPV6 || proto == IPPROTO_ICMP;
}

// Whether an ICMP packet that is no error is an echo, request or reply:
// the one message the translation carries besides errors.
//
// Any other informational message — neighbour discovery, multicast
// listener discovery, router renumbering — is dropped before it reaches
// the tables and counted on its own: the translation refuses it as well,
// without counting it.
static inline bool
nat64s_is_icmp_echo(struct packet *packet) {
	if (!nat64s_transport_fits(packet, sizeof(struct rte_icmp_hdr))) {
		return false;
	}
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
		mbuf, struct yanet_icmp6_hdr *, packet->transport_header.offset
	);
	uint8_t type = icmp->icmp6_type;
	if (packet->transport_header.type == IPPROTO_ICMPV6) {
		return type == ICMP6_ECHO_REQUEST || type == ICMP6_ECHO_REPLY;
	}
	return type == ICMP_ECHO || type == ICMP_ECHOREPLY;
}

// Whether the packet is an ICMP error: an ICMPv6 message below type 128
// or an ICMPv4 Destination Unreachable / Time Exceeded / Parameter
// Problem. Errors carry no flow identity of their own; the session is
// found through the packet they quote.
//
// A header cut short of its type byte counts as one: the error handler
// checks the quote against the packet's length and drops it as malformed.
static inline bool
nat64s_is_icmp_error(struct packet *packet) {
	uint16_t proto = packet->transport_header.type;
	if (proto != IPPROTO_ICMPV6 && proto != IPPROTO_ICMP) {
		return false;
	}
	if (!nat64s_transport_fits(packet, sizeof(struct rte_icmp_hdr))) {
		return true;
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

// The most extension headers walked in the packet an error quotes; the
// shared core walks no more when it translates that packet.
#define NAT64S_MAX_QUOTED_EXTENSIONS 4

// Whether the header is one whose length the quote walker can step over.
static inline bool
nat64s_is_quoted_extension(uint16_t next_header) {
	return next_header == IPPROTO_HOPOPTS ||
	       next_header == IPPROTO_ROUTING || next_header == IPPROTO_DSTOPTS;
}

// The transport header of the IPv6 packet quoted in an error, found past
// its extension headers. Returns false when the chain is cut short or too
// long, or ends in a fragment: none of them carries a session's transport
// identity where this module can read it.
static inline bool
nat64s_quoted_transport(
	const uint8_t *base,
	size_t ip_off,
	size_t data_len,
	uint16_t *next_header,
	size_t *offset
) {
	*offset = ip_off + sizeof(struct rte_ipv6_hdr);
	*next_header = base[ip_off + offsetof(struct rte_ipv6_hdr, proto)];
	for (size_t count = 0; nat64s_is_quoted_extension(*next_header);
	     ++count) {
		if (count == NAT64S_MAX_QUOTED_EXTENSIONS ||
		    *offset + sizeof(struct yanet_ipv6_ext_2byte) > data_len) {
			return false;
		}
		const struct yanet_ipv6_ext_2byte *extension =
			(const struct yanet_ipv6_ext_2byte *)(base + *offset);
		*next_header = extension->next_header;
		*offset += ((size_t)extension->extension_length + 1) * 8;
	}
	return *next_header != IPPROTO_FRAGMENT && *offset <= data_len;
}

// The lan side of an ICMPv6 error: find the session of the IPv6 packet
// the error quotes, then rewrite the quoted client address to
// prefix + pool-address so the shared core's embedded translation (which
// derives IPv4 addresses from the address's last four bytes) maps it to
// the session's public address. The outer packet translates with the same
// session's address as its source.
//
// An error reaching the translator through its lan side was sent to the
// remote, so the packet it quotes is inbound — from the remote under the
// session's prefix to the client — and the remote sent it to the
// allocated port, not the client's own. Errors never create or refresh
// sessions: an error about an expired flow is dropped, not resurrected.
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
	// An error is addressed to the sender of the packet it quotes: the
	// remote, under one of the configured prefixes. The shared core takes
	// the translated destination from the address's last four bytes, so
	// an error addressed anywhere else is not handed to it.
	struct rte_ipv6_hdr *outer = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	if (nat64s_dst_prefix(config, outer->dst_addr) == NULL) {
		nat64s_stats_bump(stats, NAT64S_NO_PREFIX);
		return -1;
	}
	if (emb_off + sizeof(struct rte_ipv6_hdr) > data_len) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}
	struct rte_ipv6_hdr *emb =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, emb_off);
	if (memcmp(outer->dst_addr, emb->src_addr, 16) != 0) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}

	const uint8_t *base = (const uint8_t *)mbuf->buf_addr + mbuf->data_off;
	uint16_t proto;
	size_t l4_off;
	if (!nat64s_quoted_transport(
		    base, emb_off, data_len, &proto, &l4_off
	    )) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}
	proto = nat64s_session_proto(proto);
	uint16_t sport;
	uint16_t dport;
	if (!nat64s_embedded_ports(
		    base, l4_off, proto, data_len, &sport, &dport
	    )) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}

	struct nat64state_lan_key key = {
		.hdr =
			{
				.proto = proto,
				.src_port = dport,
				.dst_port = sport,
			},
	};
	memcpy(key.src_addr, emb->dst_addr, 16);
	memcpy(key.dst_addr, emb->src_addr, 16);

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
	nat64s_lookup_unlock(lock);

	// Point the quoted client address at the session's public address,
	// under the session's own prefix, and its port at the allocated
	// port: the quote must show the tuple the remote actually sent.
	memcpy(emb->dst_addr, prefix, NAT64S_PREFIX_LEN);
	memcpy(emb->dst_addr + NAT64S_PREFIX_LEN, &pool_ip4, sizeof(pool_ip4));
	if (nat64s_patch_quoted_port(
		    mbuf, l4_off, proto, NAT64S_PORT_DEST, alloc_port
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

// The wan side of an ICMPv4 error: find the session of the outbound packet
// the error quotes. The shared core maps both the quoted pool-side
// address and the outer destination to the client, so passing the
// session's address suffices.
//
// An error reaching the translator through its wan side was sent to the
// session's public address, so the packet it quotes is the client's
// outbound one — source address and port the public ones — and the client
// must see its own port back in it. Errors never create or refresh
// sessions.
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
	// An error is addressed to the sender of the packet it quotes, the
	// session's public address: the shared core maps the outer
	// destination to the client whatever its value.
	struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	if (outer->dst_addr != emb->src_addr) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}
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

	struct nat64state_wan_key key = {
		.hdr =
			{
				.proto = proto,
				.src_port = sport,
				.dst_port = dport,
			},
		.local_addr = emb->src_addr,
		.remote_addr = emb->dst_addr,
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
	memcpy(src6, session->src6, 16);
	memcpy(prefix, session->prefix, NAT64S_PREFIX_LEN);
	nat64s_lookup_unlock(lock);

	if (nat64s_patch_quoted_port(
		    mbuf, emb_off + emb_ihl, proto, NAT64S_PORT_SOURCE, cli_port
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

// The identity a session's entries carry: the client endpoint under the
// prefix its outbound packets use.
struct nat64s_session_identity {
	const uint8_t *src6;
	const uint8_t *prefix;
	uint16_t cli_port;
};

// Whether a stored entry is the given client's under the given prefix.
//
// Read on the entry found under a session's own external tuple, that
// settles whether the tuple is the session's: the key carries the rest
// of the flow — protocol, remote port and remote address — so only this
// client towards this remote can have written it.
static inline bool
nat64s_entry_is_clients(
	const struct nat64state_value *stored,
	const struct nat64s_session_identity *identity
) {
	return stored != NULL && stored->cli_port == identity->cli_port &&
	       memcmp(stored->src6, identity->src6, 16) == 0 &&
	       memcmp(stored->prefix, identity->prefix, NAT64S_PREFIX_LEN) == 0;
}

// Find a free external port for a new session, stepping from the allocated
// one through the window, wrapping, for a bounded number of probes; the
// port found is stored back and written into the key.
//
// Returns false when every probed port is taken towards the remote by
// another session. A port the session's own entry holds is kept, so that
// a first packet whose lan insert found no room, and another worker on
// the same first packet, leave no unpaired entry behind and the session
// stays on one port.
static inline bool
nat64s_probe_wan_port(
	struct nat64s_prepared *prepared,
	const struct nat64s_module_config *config,
	uint64_t now,
	struct nat64state_wan_key *wan_key,
	uint16_t *port,
	const struct nat64s_session_identity *identity
) {
	uint32_t tries = config->port_hi - config->port_lo;
	if (tries > NAT64S_MAX_PORT_PROBES) {
		tries = NAT64S_MAX_PORT_PROBES;
	}
	for (; tries-- > 0;) {
		void *found = NULL;
		rwlock_t *lock = NULL;
		bool from_stale = false;
		int64_t taken = fwtable_lookup(
			prepared->wan.table,
			now,
			wan_key,
			&found,
			&lock,
			&from_stale
		);
		bool own =
			taken >= 0 && nat64s_entry_is_clients(found, identity);
		nat64s_lookup_unlock(lock);
		if (taken < 0 || own) {
			return true;
		}
		// The port is 16 bits wide while the window may end one past
		// the last port: past the top the step starts over at the first
		// port, instead of counting past what a port can hold.
		if ((uint32_t)*port + 1 >= config->port_hi) {
			*port = config->port_lo;
		} else {
			++*port;
		}
		wan_key->hdr.src_port = *port;
		// An echo's remote port is the same identifier as its local
		// one, so its key moves on both: the tuple probed is the
		// tuple the session holds.
		if (nat64s_is_icmp(wan_key->hdr.proto)) {
			wan_key->hdr.dst_port = *port;
		}
	}
	return false;
}

// Whether the entry an insert just stored in the given table layer
// belongs to the given session.
//
// The identity the value ops keep on a merge is read back before the
// insert's write lock goes, so a concurrent refresh cannot swap it
// underneath.
static inline bool
nat64s_wan_entry_owned(
	fwmap_t *layer,
	int64_t idx,
	const struct nat64s_session_identity *identity
) {
	return nat64s_entry_is_clients(
		(const struct nat64state_value *)
			fwmap_get_value(layer, (uint32_t)idx),
		identity
	);
}

// Whether the entry an insert just stored is latched as an established
// TCP session.
//
// It is read back under the insert's write lock from the layer captured
// before the insert; an entry that landed in a layer prepended meanwhile
// reads as not latched.
static inline bool
nat64s_entry_established(fwmap_t *layer, int64_t idx) {
	const struct nat64state_value *stored =
		(const struct nat64state_value *)fwmap_get_value(
			layer, (uint32_t)idx
		);
	return stored != NULL && stored->tcp_established;
}

// The session as the packet at hand sees it, with the lifetimes the
// configuration gives its two states.
struct nat64s_session_view {
	uint64_t worker_idx;
	uint64_t now;
	// Turned true once an entry is found latched, for the entries
	// refreshed after it.
	bool established;
	uint64_t general_ttl;
	uint64_t established_ttl;
};

// Insert or refresh one entry of a session on the lifetime its latch
// calls for.
//
// A packet of the session's other side can establish the session between
// this packet's lookup and this insert; the merge then keeps the latch but
// takes this packet's shorter lifetime. The entry is therefore read back
// under the insert's lock and, latched while the packet saw the session
// unestablished, inserted again on the established timeout. The session
// reads as established from then on, for the entries refreshed after this
// one. A failed insert releases its bucket's lock and reports false.
static inline bool
nat64s_insert_latched(
	fwtable_t *table,
	const void *key,
	struct nat64state_value *entry,
	struct nat64s_session_view *view
) {
	uint64_t ttl =
		view->established ? view->established_ttl : view->general_ttl;
	entry->tcp_established = view->established;
	entry->last_ttl = ttl;
	fwmap_t *layer = ATOMIC_ADDR_OF(&table->head);
	rwlock_t *lock = NULL;
	int64_t idx = fwtable_insert(
		table, view->worker_idx, view->now, ttl, key, entry, &lock
	);
	if (idx < 0) {
		nat64s_insert_unlock(lock);
		return false;
	}
	bool latched = ATOMIC_ADDR_OF(&table->head) == layer &&
		       nat64s_entry_established(layer, idx);
	rwlock_write_unlock(lock);

	if (!latched || view->established) {
		return true;
	}
	view->established = true;
	entry->tcp_established = true;
	entry->last_ttl = view->established_ttl;
	lock = NULL;
	if (fwtable_insert(
		    table,
		    view->worker_idx,
		    view->now,
		    view->established_ttl,
		    key,
		    entry,
		    &lock
	    ) < 0) {
		nat64s_insert_unlock(lock);
		return false;
	}
	rwlock_write_unlock(lock);
	return true;
}

// The lan side: translate an IPv6 flow to IPv4, creating or refreshing its
// session in both tables. A new session picks its public address from the
// pool by a hash of the client endpoint, and its port inside the
// configured window; an existing one keeps its allocation (the value ops
// enforce first-writer-wins on refresh).
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

	// RFC 6146 3.5: a source inside a configured prefix is hairpinned
	// traffic, not a client flow — translating it again loops. Errors
	// are held to it before anything is looked up on their behalf.
	if (nat64s_dst_prefix(config, ipv6->src_addr) != NULL) {
		nat64s_stats_bump(stats, NAT64S_HAIRPIN);
		return -1;
	}
	if (nat64s_wkp_hides_non_global(ipv6->src_addr)) {
		nat64s_stats_bump(stats, NAT64S_NON_GLOBAL);
		return -1;
	}

	if (nat64s_is_icmp_error(packet)) {
		return nat64s_handle_v6_error(config, prepared, now, packet);
	}
	if (packet->transport_header.type == IPPROTO_ICMPV6 &&
	    !nat64s_is_icmp_echo(packet)) {
		nat64s_stats_bump(stats, NAT64S_ICMP_UNSUPPORTED);
		return -1;
	}

	uint16_t proto;
	uint16_t sport;
	uint16_t dport;
	if (nat64s_transport_of(packet, &proto, &sport, &dport)) {
		LOG_DBG(NAT64,
			"nat64s: transport header not whole in the head\n");
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}

	struct nat64state_lan_key key = {
		.hdr = {.proto = nat64s_session_proto(proto),
			.src_port = sport,
			.dst_port = dport},
	};
	rte_memcpy(key.src_addr, ipv6->src_addr, 16);
	rte_memcpy(key.dst_addr, ipv6->dst_addr, 16);

	const uint8_t *prefix = nat64s_dst_prefix(config, ipv6->dst_addr);
	if (prefix == NULL) {
		// The destination embeds under no configured prefix: nothing
		// to translate.
		nat64s_stats_bump(stats, NAT64S_NO_PREFIX);
		return -1;
	}
	if (nat64s_wkp_hides_non_global(ipv6->dst_addr)) {
		nat64s_stats_bump(stats, NAT64S_NON_GLOBAL);
		return -1;
	}

	struct nat64state_value value = {0};
	rwlock_t *lock = NULL;
	void *found = NULL;
	bool from_stale = false;
	bool created = fwtable_lookup(
			       prepared->lan.table,
			       now,
			       &key,
			       &found,
			       &lock,
			       &from_stale
		       ) < 0;
	bool established = false;
	if (!created) {
		const struct nat64state_value *existing = found;
		value.ip4 = existing->ip4;
		value.port = existing->port;
		// Establishment latches, and only the remote's answer latches
		// it: until then the session ages on the general timeout,
		// whatever its client sends.
		established = existing->tcp_established;
		memcpy(value.prefix, existing->prefix, NAT64S_PREFIX_LEN);
		nat64s_lookup_unlock(lock);
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
	struct nat64s_session_view view = {
		.worker_idx = worker_idx,
		.now = now,
		.established = established,
		.general_ttl =
			nat64state_entry_ttl(proto, false, &config->timeouts),
		.established_ttl =
			nat64state_entry_ttl(proto, true, &config->timeouts),
	};

	// Both directions are refreshed on every packet: return traffic only
	// reads the wan entry, so the lan side is what keeps an active flow's
	// reverse mapping alive.
	//
	// The wan entry goes first. Should the lan insert then fail, the wan
	// entry stays as the session's own: the next packet keeps its port
	// and completes the pair. The opposite order would leave a lan entry
	// whose flow can never be answered until it expires.
	uint32_t remote_addr = 0;
	memcpy(&remote_addr, &ipv6->dst_addr[12], sizeof(remote_addr));
	uint16_t remote_port = dport;
	if (nat64s_is_icmp(proto)) {
		// An echo is answered under the identifier it left with, so
		// both ports of its wan key are the allocated one.
		remote_port = value.port;
	}
	struct nat64state_wan_key wan_key = {
		.hdr =
			{
				.proto = nat64s_session_proto(proto),
				.src_port = value.port,
				.dst_port = remote_port,
			},
		.local_addr = value.ip4,
		.remote_addr = remote_addr,
	};

	// The external tuple is unique per client: when the allocated address
	// and port are already taken towards this remote by another flow —
	// the same in-window source port, or a hash collision — the
	// neighboring ports of the window are probed before anything is
	// recorded.
	//
	// A flow that finds every probed port taken gives up: a shared tuple
	// would hand its return traffic to another client.

	// The client's address is kept apart from the packet: the
	// translation rewrites the network header it was read from.
	uint8_t src6[16];
	memcpy(src6, ipv6->src_addr, sizeof(src6));
	struct nat64s_session_identity identity = {
		.src6 = src6,
		.prefix = prefix,
		.cli_port = sport,
	};
	if (created &&
	    !nat64s_probe_wan_port(
		    prepared, config, now, &wan_key, &value.port, &identity
	    )) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	struct nat64state_value wan_value = {0};
	wan_value.updated_at = now;
	wan_value.last_ttl = ttl;
	wan_value.tcp_established = established;
	memcpy(wan_value.prefix, prefix, NAT64S_PREFIX_LEN);
	wan_value.cli_port = sport;
	memcpy(wan_value.src6, src6, sizeof(src6));

	// The packet translates before anything is recorded, under the port
	// the session is to store.
	//
	// The translation refuses some packets itself — a routing header of
	// the deprecated type 0, a header chain it rejects — and such a
	// packet must not take session slots: a slot spent on a packet that
	// never leaves stays spent for as long as the layer lives.
	if (value.port != sport &&
	    nat64s_patch_source_port(packet, value.port)) {
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
		return -1;
	}
	struct nat64_ctx ctx = nat64s_ctx(config);
	if (nat64_translate_v6_to_v4(&ctx, packet, value.ip4) != 0) {
		return -1;
	}

	// The entry is read back from the layer it lands in; a layer
	// prepended meanwhile answers with another entry, so the insert is
	// not trusted.
	fwmap_t *wan_layer = ATOMIC_ADDR_OF(&prepared->wan.table->head);
	lock = NULL;
	int64_t wan_idx = fwtable_insert(
		prepared->wan.table,
		worker_idx,
		now,
		ttl,
		&wan_key,
		&wan_value,
		&lock
	);
	if (wan_idx < 0) {
		nat64s_insert_unlock(lock);
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}
	// Two first packets can race onto a tuple both found free, and the
	// loser merged into the winner's entry must not publish its own lan
	// entry on top of it.
	//
	// Between two client endpoints that entry would send the second
	// client's packets under the first one's external tuple; between two
	// prefixes of one endpoint its replies would return under the
	// winner's prefix, to a flow that never opened.
	bool same_layer =
		ATOMIC_ADDR_OF(&prepared->wan.table->head) == wan_layer;
	bool owned = same_layer &&
		     nat64s_wan_entry_owned(wan_layer, wan_idx, &identity);
	bool latched =
		same_layer && nat64s_entry_established(wan_layer, wan_idx);
	rwlock_write_unlock(lock);
	if (!owned) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}

	if (latched && !established) {
		// Established by the session's other side since this packet's
		// lookup: inserted again on the established timeout.
		view.established = true;
		wan_value.tcp_established = true;
		wan_value.last_ttl = view.established_ttl;
		lock = NULL;
		if (fwtable_insert(
			    prepared->wan.table,
			    worker_idx,
			    now,
			    view.established_ttl,
			    &wan_key,
			    &wan_value,
			    &lock
		    ) < 0) {
			nat64s_insert_unlock(lock);
			nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
			return -1;
		}
		rwlock_write_unlock(lock);
	}

	if (!nat64s_insert_latched(prepared->lan.table, &key, &value, &view)) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}

	// Both entries stand behind the packet already translated.
	nat64s_stats_bump(stats, NAT64S_TRANSLATED_V6_TO_V4);
	return 0;
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
	if (packet->transport_header.type == IPPROTO_ICMP &&
	    !nat64s_is_icmp_echo(packet)) {
		nat64s_stats_bump(stats, NAT64S_ICMP_UNSUPPORTED);
		return -1;
	}

	uint16_t proto;
	uint16_t sport;
	uint16_t dport;
	if (nat64s_transport_of(packet, &proto, &sport, &dport)) {
		LOG_DBG(NAT64,
			"nat64s: transport header not whole in the head\n");
		nat64s_stats_bump(stats, NAT64S_MALFORMED);
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
	nat64s_lookup_unlock(lock);

	// Establishment latches here too: the remote's SYN answering the
	// handshake moves the session onto the established timeout.
	struct nat64s_session_view view = {
		.worker_idx = worker_idx,
		.now = now,
		.established =
			established || nat64s_tcp_establishes_wan(packet),
		.general_ttl =
			nat64state_entry_ttl(proto, false, &config->timeouts),
		.established_ttl =
			nat64state_entry_ttl(proto, true, &config->timeouts),
	};

	// Return traffic keeps its own entry alive: a flow whose client is
	// silent for a while must not lose its reverse mapping while the
	// remote still talks.
	struct nat64state_value refresh = {0};
	refresh.updated_at = now;
	refresh.cli_port = cli_port;
	rte_memcpy(refresh.src6, src6, sizeof(src6));
	if (!nat64s_insert_latched(
		    prepared->wan.table, &key, &refresh, &view
	    )) {
		nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
		return -1;
	}

	// Both entries of a session age together: the lan entry is what the
	// client's next packet looks up, so return traffic alone must keep
	// it alive too, or a client that stays silent loses its entry while
	// the remote is still talking. Its key is the client's original
	// flow, rebuilt from what the session stores.
	//
	// The value carries the whole session rather than only the fields
	// the merge preserves: a lan entry that already expired is recreated
	// by this insert into an empty slot, where no merge runs, and it
	// must come back with its external identity. That identity is part
	// of the key this path matched.
	//
	// The client's next packet then finds this entry and refreshes it
	// instead of creating a session, so the SYN-only rule for a new
	// session does not apply to it: the wan lookup this path already
	// made is what proves the session exists.
	if (prepared->lan.table != NULL) {
		// An echo's original flow carried its own identifier as both
		// ports.
		uint16_t lan_remote_port =
			nat64s_is_icmp(proto) ? cli_port : sport;
		struct nat64state_lan_key lan_key = {
			.hdr =
				{
					.proto = nat64s_session_proto(proto),
					.src_port = cli_port,
					.dst_port = lan_remote_port,
				},
		};
		memcpy(lan_key.src_addr, src6, sizeof(lan_key.src_addr));
		memcpy(lan_key.dst_addr, prefix, NAT64S_PREFIX_LEN);
		memcpy(lan_key.dst_addr + NAT64S_PREFIX_LEN,
		       &ipv4->src_addr,
		       sizeof(uint32_t));

		struct nat64state_value lan_refresh = {0};
		lan_refresh.updated_at = now;
		lan_refresh.ip4 = key.local_addr;
		lan_refresh.port = key.hdr.src_port;
		lan_refresh.cli_port = cli_port;
		rte_memcpy(lan_refresh.src6, src6, sizeof(lan_refresh.src6));
		rte_memcpy(
			lan_refresh.prefix, prefix, sizeof(lan_refresh.prefix)
		);
		if (!nat64s_insert_latched(
			    prepared->lan.table, &lan_key, &lan_refresh, &view
		    )) {
			nat64s_stats_bump(stats, NAT64S_INSERT_FAILED);
			return -1;
		}
	}

	// The client's own port is restored even when it is zero: an echo
	// leaves with a translated identifier like any other outside the
	// window, and its reply must come back under the client's own.
	if (cli_port != dport && nat64s_patch_dest_port(packet, cli_port)) {
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

		// Every fragment of a fragmented datagram is refused, the first
		// one included: a session keys on the transport identity, which
		// only the first fragment carries, and no session can serve the
		// rest.
		if (packet->fragment_offset != 0 ||
		    (packet->flags & (1 << PACKET_FLAG_FRAGMENTED)) != 0) {
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
