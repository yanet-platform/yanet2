#pragma once

#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_mbuf_core.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "checksum.h"

////////////////////////////////////////////////////////////////////////////////

/// @todo: move this logic

#define TCP_OPTION_MSS_LEN (4)
#define TCP_OPTION_KIND_MSS (2)
#define TCP_OPTION_KIND_EOL (0)
#define TCP_OPTION_KIND_NOP (1)

#define DEFAULT_MSS_SIZE 536
#define FIX_MSS_SIZE 1220

////////////////////////////////////////////////////////////////////////////////

struct tcp_option {
	uint8_t kind;
	uint8_t len;
	char data[0];
} __attribute__((__packed__));

////////////////////////////////////////////////////////////////////////////////

static inline void
fix_mss_ipv6(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		if ((tcp_header->tcp_flags &
		     (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)) !=
		    RTE_TCP_SYN_FLAG) {
			return;
		}

		uint16_t tcp_data_offset = (tcp_header->data_off >> 4) * 4;
		if (tcp_data_offset < sizeof(struct rte_tcp_hdr) ||
		    packet->transport_header.offset + tcp_data_offset >
			    rte_pktmbuf_pkt_len(mbuf)) {
			// Data offset is out of bounds of the packet, nothing
			// to do here
			return;
		}

		// Option lookup
		uint16_t tcp_option_offset = sizeof(struct rte_tcp_hdr);
		while (tcp_option_offset + TCP_OPTION_MSS_LEN <= tcp_data_offset
		) {
			const struct tcp_option *option =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct tcp_option *,
					packet->transport_header.offset +
						tcp_option_offset
				);

			if (option->kind == TCP_OPTION_KIND_MSS) {
				/// mss could not be increased so check the
				/// value first
				uint16_t old_mss = rte_be_to_cpu_16(
					*(uint16_t *)option->data
				);
				if (old_mss <= FIX_MSS_SIZE) {
					return;
				}
				uint16_t cksum = ~tcp_header->cksum;
				cksum = csum_minus(
					cksum, *(uint16_t *)option->data
				);
				*(uint16_t *)option->data =
					rte_cpu_to_be_16(FIX_MSS_SIZE);
				cksum = csum_plus(
					cksum, *(uint16_t *)option->data
				);
				tcp_header->cksum =
					(cksum == 0xffff) ? cksum : ~cksum;
				return;
			} else if (option->kind == TCP_OPTION_KIND_EOL ||
				   option->kind == TCP_OPTION_KIND_NOP) {
				tcp_option_offset++;
			} else {
				if (option->len == 0) {
					/// packet header is broken
					return;
				}
				tcp_option_offset += option->len;
			}
		}

		/// try to insert option
		if (tcp_data_offset > (0x0f << 2) - TCP_OPTION_MSS_LEN) {
			/// no space to insert the option
			return;
		}

		/// insert option just after regular tcp header
		rte_pktmbuf_prepend(mbuf, TCP_OPTION_MSS_LEN);
		memmove(rte_pktmbuf_mtod(mbuf, char *),
			rte_pktmbuf_mtod_offset(
				mbuf, char *, TCP_OPTION_MSS_LEN
			),
			packet->transport_header.offset +
				sizeof(struct rte_tcp_hdr));
		struct tcp_option *option = rte_pktmbuf_mtod_offset(
			mbuf,
			struct tcp_option *,
			packet->transport_header.offset +
				sizeof(struct rte_tcp_hdr)
		);
		option->kind = TCP_OPTION_KIND_MSS;
		option->len = TCP_OPTION_MSS_LEN;
		*(uint16_t *)option->data = rte_cpu_to_be_16(DEFAULT_MSS_SIZE);

		/// adjust tcp and ip lengths and update checksums
		tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		tcp_header->data_off += 0x1 << 4;
		uint16_t cksum = ~tcp_header->cksum;
		/// data_off is the leading byte of corresponding 2-byte
		/// sequence inside a tcp header so there is no rte_cpu_to_be_16
		cksum = csum_plus(cksum, 0x1 << 4);
		cksum = csum_plus(cksum, *(uint16_t *)option);
		cksum = csum_plus(cksum, *(uint16_t *)option->data);
		cksum = csum_plus(cksum, rte_cpu_to_be_16(TCP_OPTION_MSS_LEN));
		tcp_header->cksum = (cksum == 0xffff) ? cksum : ~cksum;

		struct rte_ipv6_hdr *ipv6_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		ipv6_header->payload_len = rte_cpu_to_be_16(
			rte_be_to_cpu_16(ipv6_header->payload_len) +
			TCP_OPTION_MSS_LEN
		);
	}
}

#undef TCP_OPTION_MSS_LEN
#undef TCP_OPTION_KIND_MSS
#undef TCP_OPTION_KIND_EOL
#undef TCP_OPTION_KIND_NOP

#undef DEFAULT_MSS_SIZE
#undef FIX_MSS_SIZE