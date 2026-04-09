#include <netinet/in.h>
#include <string.h>

#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

#include "common/checksum.h"

#include "lib/dataplane/packet/packet.h"

#include "mss.h"

#define TCP_OPTION_KIND_EOL 0
#define TCP_OPTION_KIND_NOP 1
#define TCP_OPTION_KIND_MSS 2
#define TCP_OPTION_MSS_LEN 4

/* MSS value to clamp down to (accounts for tunnel overhead on IPv6). */
#define FIX_MSS_SIZE 1220

/* MSS value to insert when no MSS option is present. */
#define DEFAULT_MSS_SIZE 536

/* Maximum TCP data offset in 32-bit words (4-bit field). */
#define TCP_DATA_OFF_MAX 0x0F

struct tcp_option {
	uint8_t kind;
	uint8_t len;
	uint8_t data[];
} __attribute__((__packed__));

/*
 * Check whether the packet is a TCP SYN (without RST) that may
 * carry MSS options. Returns the TCP header pointer, or NULL
 * if the packet should be skipped.
 */
static struct rte_tcp_hdr *
get_syn_tcp_header(struct packet *packet) {
	if (unlikely(packet->transport_header.type != IPPROTO_TCP)) {
		return NULL;
	}

	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_tcp_hdr *,
		packet->transport_header.offset
	);

	uint8_t flags = tcp->tcp_flags;
	if ((flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)) !=
	    RTE_TCP_SYN_FLAG) {
		return NULL;
	}

	return tcp;
}

/*
 * Validate the TCP data offset and return the options length in bytes.
 * Returns 0 if the data offset is invalid or out of packet bounds.
 */
static uint16_t
tcp_options_length(struct packet *packet, struct rte_tcp_hdr *tcp) {
	uint16_t data_offset = (tcp->data_off >> 4) * 4;

	if (data_offset < sizeof(struct rte_tcp_hdr)) {
		return 0;
	}

	uint16_t pkt_len = rte_pktmbuf_pkt_len(packet->mbuf);
	if (packet->transport_header.offset + data_offset > pkt_len) {
		return 0;
	}

	return data_offset;
}

/*
 * Scan TCP options for an existing MSS option.
 * If found and its value exceeds FIX_MSS_SIZE, clamp it and
 * update the TCP checksum incrementally.
 *
 * Returns true if an MSS option was found (regardless of whether
 * it was modified), false if no MSS option exists.
 */
static bool
try_clamp_existing_mss(
	struct packet *packet, struct rte_tcp_hdr *tcp, uint16_t data_offset
) {
	uint16_t offset = sizeof(struct rte_tcp_hdr);

	while (offset + TCP_OPTION_MSS_LEN <= data_offset) {
		struct tcp_option *opt = rte_pktmbuf_mtod_offset(
			packet->mbuf,
			struct tcp_option *,
			packet->transport_header.offset + offset
		);

		if (opt->kind == TCP_OPTION_KIND_MSS) {
			uint16_t old_mss =
				rte_be_to_cpu_16(*(uint16_t *)opt->data);
			if (old_mss <= FIX_MSS_SIZE) {
				return true;
			}

			/* Clamp MSS and update checksum incrementally. */
			uint16_t cksum = ~tcp->cksum;
			cksum = csum_minus(cksum, *(uint16_t *)opt->data);
			*(uint16_t *)opt->data = rte_cpu_to_be_16(FIX_MSS_SIZE);
			cksum = csum_plus(cksum, *(uint16_t *)opt->data);
			tcp->cksum = (cksum == 0xffff) ? cksum : ~cksum;
			return true;
		}

		if (opt->kind == TCP_OPTION_KIND_EOL ||
		    opt->kind == TCP_OPTION_KIND_NOP) {
			offset++;
		} else {
			if (opt->len == 0) {
				return false; /* malformed header */
			}
			offset += opt->len;
		}
	}

	return false;
}

/*
 * Insert a new MSS option (DEFAULT_MSS_SIZE) right after the fixed
 * TCP header. Shifts L2 + L3 + fixed TCP header backward by 4 bytes,
 * then writes the option into the gap.
 *
 * Updates TCP data offset, TCP checksum, and IPv6 payload length.
 */
static void
insert_mss_option(struct packet *packet, struct rte_tcp_hdr *tcp) {
	uint16_t data_offset = (tcp->data_off >> 4) * 4;

	/* Check if there is room for one more 32-bit option word. */
	if (data_offset > (TCP_DATA_OFF_MAX << 2) - TCP_OPTION_MSS_LEN) {
		return;
	}

	struct rte_mbuf *mbuf = packet->mbuf;

	/* Extend the packet at the front by TCP_OPTION_MSS_LEN bytes. */
	if (rte_pktmbuf_prepend(mbuf, TCP_OPTION_MSS_LEN) == NULL) {
		return;
	}

	/* Move everything before the TCP options backward. */
	uint16_t prefix_len =
		packet->transport_header.offset + sizeof(struct rte_tcp_hdr);
	memmove(rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, TCP_OPTION_MSS_LEN),
		prefix_len);

	/* Write the MSS option into the gap. */
	struct tcp_option *opt =
		rte_pktmbuf_mtod_offset(mbuf, struct tcp_option *, prefix_len);
	opt->kind = TCP_OPTION_KIND_MSS;
	opt->len = TCP_OPTION_MSS_LEN;
	*(uint16_t *)opt->data = rte_cpu_to_be_16(DEFAULT_MSS_SIZE);

	/* Re-fetch TCP header (it moved due to prepend). */
	tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, packet->transport_header.offset
	);

	/* Increment data offset by one 32-bit word. */
	tcp->data_off += 0x1 << 4;

	/*
	 * Update TCP checksum incrementally.
	 *
	 * data_off is the leading byte of its 16-bit aligned pair
	 * inside the TCP header, so no byte-swap is needed for the
	 * data_off delta.
	 */
	uint16_t cksum = ~tcp->cksum;
	cksum = csum_plus(cksum, 0x1 << 4);
	cksum = csum_plus(cksum, *(uint16_t *)opt);
	cksum = csum_plus(cksum, *(uint16_t *)opt->data);
	cksum = csum_plus(cksum, rte_cpu_to_be_16(TCP_OPTION_MSS_LEN));
	tcp->cksum = (cksum == 0xffff) ? cksum : ~cksum;

	/* Update IPv6 payload length. */
	struct rte_ipv6_hdr *ip6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	ip6->payload_len = rte_cpu_to_be_16(
		rte_be_to_cpu_16(ip6->payload_len) + TCP_OPTION_MSS_LEN
	);
}

void
fix_mss_ipv6(struct packet *packet) {
	struct rte_tcp_hdr *tcp = get_syn_tcp_header(packet);
	if (tcp == NULL) {
		return;
	}

	uint16_t data_offset = tcp_options_length(packet, tcp);
	if (data_offset == 0) {
		return;
	}

	if (!try_clamp_existing_mss(packet, tcp, data_offset)) {
		insert_mss_option(packet, tcp);
	}
}
