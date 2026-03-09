#pragma once

/**
 * @file icmp.h
 * @brief Packed ICMP header structures for safe mbuf access.
 *
 * This header provides packed ICMPv4 and ICMPv6 header structures that can be
 * safely accessed at arbitrary offsets in DPDK mbuf data. System headers
 * like <netinet/icmp6.h> define structures without __packed attribute,
 * which causes UBSAN warnings and potential crashes on misaligned access.
 *
 * Use these structures when accessing ICMP headers from mbuf data via
 * rte_pktmbuf_mtod_offset() or similar functions.
 *
 * @note All structures use __rte_packed attribute for safe unaligned access.
 */

#include <stdint.h>

#include <rte_byteorder.h>

/* ICMPv6 packed header for safe unaligned mbuf access.
 * Based on system struct icmp6_hdr but with packed attribute.
 * This prevents UBSAN alignment warnings when accessing ICMPv6 headers
 * at arbitrary offsets in mbuf data.
 */
struct yanet_icmp6_hdr {
	uint8_t icmp6_type;
	uint8_t icmp6_code;
	rte_be16_t icmp6_cksum;
	union {
		rte_be32_t icmp6_un_data32[1];
		rte_be16_t icmp6_un_data16[2];
		uint8_t icmp6_un_data8[4];
	} icmp6_dataun;
} __attribute__((__packed__));

/* Convenience macros for accessing ICMPv6 header fields via -> operator */
#define yanet_icmp6_pptr icmp6_dataun.icmp6_un_data32[0]
#define yanet_icmp6_mtu icmp6_dataun.icmp6_un_data32[0]
#define yanet_icmp6_id icmp6_dataun.icmp6_un_data16[0]
#define yanet_icmp6_seq icmp6_dataun.icmp6_un_data16[1]

/* ICMPv4 packed header with full union support.
 * Based on system struct icmp but with packed attribute.
 */
struct yanet_icmp_hdr {
	uint8_t icmp_type;
	uint8_t icmp_code;
	rte_be16_t icmp_cksum;
	union {
		uint8_t ih_pptr;
		struct {
			rte_be16_t ipm_void;
			rte_be16_t ipm_nextmtu;
		} ih_pmtu;
		struct {
			rte_be16_t icd_id;
			rte_be16_t icd_seq;
		} ih_idseq;
		rte_be32_t ih_void;
	} icmp_hun;
} __attribute__((__packed__));

/* Convenience macros for accessing ICMPv4 header fields via -> operator */
#define yanet_icmp_pptr icmp_hun.ih_pptr
#define yanet_icmp_nextmtu icmp_hun.ih_pmtu.ipm_nextmtu
#define yanet_icmp_id icmp_hun.ih_idseq.icd_id
#define yanet_icmp_seq icmp_hun.ih_idseq.icd_seq
