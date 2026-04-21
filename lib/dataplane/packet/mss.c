#include <netinet/in.h>
#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

#include "common/checksum.h"

#include "lib/dataplane/packet/packet.h"
#include "rte_branch_prediction.h"

#include "mss.h"

#define TCP_OPTION_KIND_EOL 0
#define TCP_OPTION_KIND_NOP 1
#define TCP_OPTION_KIND_MSS 2
#define TCP_OPTION_MSS_LEN 4

/* Minimum length of any variable-length TCP option: kind + len bytes. */
#define TCP_OPTION_MIN_LEN 2

/* Max TCP header length in bytes (data_off is 4 bits of 32-bit words: 15*4). */
#define TCP_HDR_LEN_MAX 60

/* One 32-bit-word step expressed in the raw data_off byte (high nibble). */
#define TCP_DATA_OFF_ONE_WORD (1u << 4)

struct tcp_option {
	uint8_t kind;
	uint8_t len;
	uint8_t data[];
} __attribute__((__packed__));

/*
 * Return the TCP header if the packet is IPv6 and carries a TCP SYN or
 * SYN+ACK (i.e. SYN is set and RST is clear); NULL otherwise.
 *
 * Precondition: parse_packet guarantees that whenever
 * transport_header.type == IPPROTO_TCP, the mbuf holds at least
 * `transport_header.offset + sizeof(struct rte_tcp_hdr)` bytes,
 * so the fixed 20-byte TCP header is safe to dereference here.
 */
static struct rte_tcp_hdr *
get_syn_tcp_header(struct packet *packet) {
	if (unlikely(
		    packet->network_header.type !=
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)
	    )) {
		return NULL;
	}

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
 * Validate the TCP data offset field and return the TCP header length
 * in bytes. Returns 0 if the declared length is invalid or extends past
 * the packet end.
 */
static uint16_t
tcp_header_length(struct packet *packet, struct rte_tcp_hdr *tcp) {
	uint16_t hdr_len = (tcp->data_off >> 4) * 4;

	/* Declared header shorter than the fixed TCP header => malformed. */
	if (unlikely(hdr_len < sizeof(struct rte_tcp_hdr))) {
		return 0;
	}

	/* Declared header extends past the packet end => malformed/truncated.
	 */
	uint16_t pkt_len = rte_pktmbuf_pkt_len(packet->mbuf);
	if (unlikely(packet->transport_header.offset + hdr_len > pkt_len)) {
		return 0;
	}

	return hdr_len;
}

enum find_result {
	found,
	absent,
	malformed,
};

/*
 * Walk the TCP options area looking for an MSS option. On success
 * (mss_scan_found) writes a pointer to the option into *out.
 */
static enum find_result
find_mss_option(
	struct packet *packet, uint16_t hdr_len, struct tcp_option **out
) {
	uint16_t offset = sizeof(struct rte_tcp_hdr);

	while (offset < hdr_len) {
		struct tcp_option *opt = rte_pktmbuf_mtod_offset(
			packet->mbuf,
			struct tcp_option *,
			packet->transport_header.offset + offset
		);

		/* End Of Option List: remaining bytes are padding — stop. */
		if (opt->kind == TCP_OPTION_KIND_EOL) {
			return absent;
		}

		if (opt->kind == TCP_OPTION_KIND_NOP) {
			++offset;
			continue;
		}

		/*
		 * Variable-length option: must have at least a kind+len pair,
		 * and its declared length must not overrun the options area.
		 */
		if (unlikely(
			    offset + TCP_OPTION_MIN_LEN > hdr_len ||
			    opt->len < TCP_OPTION_MIN_LEN ||
			    offset + opt->len > hdr_len
		    )) {
			return malformed;
		}

		if (opt->kind == TCP_OPTION_KIND_MSS) {
			if (unlikely(opt->len != TCP_OPTION_MSS_LEN)) {
				return malformed;
			}
			*out = opt;
			return found;
		}

		offset += opt->len;
	}

	return absent;
}

/*
 * If the MSS option value exceeds `clamp_mss`, rewrite it to `clamp_mss`
 * and patch the TCP checksum. Does nothing if the value is already low enough.
 *
 * Precondition: `opt` is a well-formed MSS option (kind=MSS, len=4).
 */
static void
clamp_mss_option(
	struct rte_tcp_hdr *tcp, struct tcp_option *opt, uint16_t clamp_mss
) {
	uint16_t *mss_ptr = (uint16_t *)opt->data;
	uint16_t old_mss = rte_be_to_cpu_16(*mss_ptr);
	if (old_mss <= clamp_mss) {
		return;
	}

	/*
	 * Incremental TCP checksum update per RFC 1624: subtract the old
	 * 16-bit word, write the new one, add it back. The `== 0xffff`
	 * guard preserves an all-ones checksum instead of flipping it to zero.
	 */
	uint16_t cksum = ~tcp->cksum;
	cksum = csum_minus(cksum, *mss_ptr);
	*mss_ptr = rte_cpu_to_be_16(clamp_mss);
	cksum = csum_plus(cksum, *mss_ptr);
	tcp->cksum = (cksum == 0xffff) ? cksum : ~cksum;
}

/*
 * Insert a new MSS option with value `insert_mss` right after the fixed
 * TCP header. Moves L2 + L3 + fixed TCP header to a lower address by
 * 4 bytes, writes the option into the freed gap, then updates the TCP
 * data offset, TCP checksum, and IPv6 payload length.
 *
 * Precondition: the packet is IPv6 (IPv6 payload length is the only L3
 * length field updated here).
 */
static enum packet_fix_mss_result
insert_mss_option(
	struct packet *packet, struct rte_tcp_hdr *tcp, uint16_t insert_mss
) {
	/* TCP header length in bytes (high nibble = 32-bit word count). */
	uint16_t hdr_len = (tcp->data_off >> 4) * 4;

	/* Not enough room in data_off to represent one more option word. */
	if (unlikely(hdr_len + TCP_OPTION_MSS_LEN > TCP_HDR_LEN_MAX)) {
		return packet_fix_mss_malformed;
	}

	struct rte_mbuf *mbuf = packet->mbuf;

	if (unlikely(rte_pktmbuf_prepend(mbuf, TCP_OPTION_MSS_LEN) == NULL)) {
		return packet_fix_mss_no_headroom;
	}

	/* Move L2 + L3 + fixed TCP header to a lower address to open a 4-byte
	 * gap. */
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
	*(uint16_t *)opt->data = rte_cpu_to_be_16(insert_mss);

	/* Re-fetch TCP header (moved due to prepend). */
	tcp = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_tcp_hdr *, packet->transport_header.offset
	);

	/* Grow the TCP header by one 32-bit word (our newly inserted MSS
	 * option). */
	tcp->data_off += TCP_DATA_OFF_ONE_WORD;

	/*
	 * Update TCP checksum incrementally (RFC 1624).
	 *
	 * `data_off` is the high byte of a 16-bit aligned pair inside the
	 * TCP header, so adding TCP_DATA_OFF_ONE_WORD to a host-order 16-bit
	 * accumulator matches the change to the byte stream. The `opt`
	 * fields are already in network order in memory, so they are
	 * summed as-is. The MSS length term compensates for the growth
	 * of the TCP length in the pseudo-header.
	 */
	uint16_t cksum = ~tcp->cksum;
	cksum = csum_plus(cksum, TCP_DATA_OFF_ONE_WORD);
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

	return packet_fix_mss_ok;
}

enum packet_fix_mss_result
packet_fix_mss(struct packet *packet, uint16_t clamp_mss, uint16_t insert_mss) {
	struct rte_tcp_hdr *tcp = get_syn_tcp_header(packet);
	if (tcp == NULL) {
		return packet_fix_mss_ok;
	}

	uint16_t hdr_len = tcp_header_length(packet, tcp);
	if (unlikely(hdr_len == 0)) {
		return packet_fix_mss_malformed;
	}

	struct tcp_option *mss_opt = NULL;
	switch (find_mss_option(packet, hdr_len, &mss_opt)) {
	case found:
		clamp_mss_option(tcp, mss_opt, clamp_mss);
		return packet_fix_mss_ok;
	case malformed:
		return packet_fix_mss_malformed;
	case absent:
		return insert_mss_option(packet, tcp, insert_mss);
	}

	__builtin_unreachable();
}
