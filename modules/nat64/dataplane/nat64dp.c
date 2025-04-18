/**
 * @file nat64dp.c
 * @brief NAT64 dataplane implementation.
 *
 * Implements stateless translation between IPv4 and IPv6 networks including:
 * - Header translation and address mapping
 * - Protocol-specific handling (TCP, UDP, ICMP)
 * - Fragmentation processing
 * - Checksum recalculation
 * - Translation of embedded packets in ICMP error messages, including
 *   recursive protocol translation of the original packet headers and payload
 *
 * References:
 * - RFC7915: IP/ICMP Translation Algorithm
 * - RFC1191: Path MTU Discovery
 * - RFC2765: Stateless IP/ICMP Translation Algorithm (SIIT)
 */
/* System headers */
#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include <sys/types.h>

/* DPDK headers */
#include <rte_common.h>
#include <rte_config.h>
#include <rte_errno.h>
#include <rte_log.h>
#include <rte_malloc.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <rte_timer.h>

/* Protocol headers */
#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

/* DPDK protocol headers */
#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

/* Project headers */
#include "common.h"
#include "dataplane/module/module.h"
#include "modules/nat64/dataplane/nat64dp.h"

/**
 * @def RTE_LOGTYPE_NAT64
 * @brief Define log type for NAT64 module.
 *
 * This macro defines a specific log type for the NAT64 module.
 * The log type is registered using RTE_LOG_REGISTER_DEFAULT with the log level
 * set to DEBUG if DEBUG_NAT64 is defined, otherwise INFO.
 *
 * @note For more details on RTE_LOG and log types, refer to the DPDK
 * documentation: https://doc.dpdk.org/guides/prog_guide/log_lib.html
 */
#ifdef DEBUG_NAT64
RTE_LOG_REGISTER_DEFAULT(nat64_logtype, DEBUG);
#else
RTE_LOG_REGISTER_DEFAULT(nat64_logtype, INFO);
#endif
#define RTE_LOGTYPE_NAT64 nat64_logtype

/**
 * @brief Finds a mapping from IPv6 to IPv4 address
 *
 * This function searches the LPM (Longest Prefix Match) table for a mapping
 * from an IPv6 address to an IPv4 address. The search is performed using the
 * IPv6 address as the lookup key.
 *
 * The function implements part of the stateless NAT64 translation algorithm
 * described in RFC7915 section 4.1 (Address Translation).
 *
 * @param config Pointer to the NAT64 module configuration containing mapping
 * tables. Must not be NULL.
 * @param ip6 Pointer to the source IPv6 address to look up (16 bytes).
 *            Must not be NULL and must point to a valid IPv6 address.
 *
 * @return Pointer to the found mapping structure on success,
 *         NULL on failure with the following possible reasons:
 *         - Invalid parameters (NULL config or ip6)
 *         - No matching prefix found in the LPM table
 *         - Invalid mapping index
 *
 * @see ip4to6
 * @see nat64_module_config
 *
 * @note The function assumes the IPv6 address is in network byte order
 *
 * @example
 * ```c
 * uint8_t ipv6_addr[16] = {
 *     0x20, 0x01, 0x0d, 0xb8, // 2001:db8::/32 prefix
 *     0x00, 0x00, 0x00, 0x00,
 *     0x00, 0x00, 0x00, 0x00,
 *     0x00, 0x00, 0x00, 0x01
 * };
 * struct ip4to6 *mapping = find_ip6to4(config, ipv6_addr);
 * if (mapping) {
 *     // Use mapping->ip4 as the translated IPv4 address
 * }
 * ```
 */
struct ip4to6 *
find_ip6to4(struct nat64_module_config *config, uint8_t *ip6) {
	if (!config || !ip6) {
		return NULL;
	}

	// Search for a match in the LPM table
	uint32_t index = lpm_lookup(&config->mappings.v6_to_v4, 16, ip6);
	if (index == LPM_VALUE_INVALID) {
		return NULL;
	}

	// Get a pointer to the corresponding entry in the mappings list
	if (index >= config->mappings.count) {
		return NULL;
	}

	return &ADDR_OF(&config->mappings.list)[index];
}

/**
 * @brief Finds a mapping from IPv4 to IPv6 address
 *
 * This function searches the LPM (Longest Prefix Match) table for a mapping
 * from an IPv4 address to an IPv6 address. The search is performed using the
 * IPv4 address as the lookup key.
 *
 * @param config Pointer to the NAT64 module configuration containing mapping
 * tables. Must not be NULL.
 * @param ip4 Pointer to the source IPv4 address to look up (4 bytes).
 *            Must not be NULL and must point to a valid IPv4 address.
 *
 * @return Pointer to the found mapping structure on success,
 *         NULL on failure with the following possible reasons:
 *         - Invalid parameters (NULL config or ip4)
 *         - No matching prefix found in the LPM table
 *         - Invalid mapping index
 *
 * @see ip4to6
 * @see nat64_module_config
 *
 * @note The function assumes the IPv4 address is in network byte order
 *
 * @example
 * ```c
 * uint32_t ipv4_addr = RTE_BE32(RTE_IPV4(192, 0, 2, 1));
 * struct ip4to6 *mapping = find_ip4to6(config, &ipv4_addr);
 * if (mapping) {
 *     // Use mapping->ip6 as the translated IPv6 address
 * }
 * ```
 */
struct ip4to6 *
find_ip4to6(struct nat64_module_config *config, uint32_t *ip4) {
	if (!config || !ip4) {
		return NULL;
	}

	// Search for a match in the LPM table
	uint32_t index =
		lpm_lookup(&config->mappings.v4_to_v6, 4, (uint8_t *)ip4);
	if (index == LPM_VALUE_INVALID) {
		return NULL;
	}

	// Get a pointer to the corresponding entry in the mappings list
	if (index >= config->mappings.count) {
		return NULL;
	}

	return &ADDR_OF(&config->mappings.list)[index];
}

/**
 * @brief Validates IPv4/IPv6 fragment parameters according to RFC7915
 *
 * This function validates fragment parameters to ensure they comply with
 * RFC7915 requirements for NAT64 translation. It performs the following checks:
 * - Rejects fragmented ICMP/ICMPv6 packets (RFC7915 section 1.2)
 * - Verifies fragment offset is a multiple of 8 bytes
 * - Ensures non-last fragments are multiples of 8 bytes
 * - Validates minimum fragment size (8 bytes)
 * - Checks for fragment overlap
 *
 * @param frag_offset Fragment offset in bytes from start of original packet.
 *                    Must be a multiple of 8 bytes.
 * @param frag_size Size of current fragment's payload in bytes.
 *                  Must be at least 8 bytes.
 *                  For non-last fragments, must be a multiple of 8 bytes.
 * @param total_len Total length of the original unfragmented packet.
 *                  Used to detect fragment overlap.
 * @param more_fragments Flag indicating if more fragments follow (MF bit).
 *                      True if this is not the last fragment.
 * @param is_icmp Flag indicating if packet is ICMP/ICMPv6.
 *                Fragmented ICMP packets are not allowed.
 *
 * @return 0 if all fragment parameters are valid,
 *         -1 if any validation fails with the following possible reasons:
 *         - Fragmented ICMP packet (not allowed)
 *         - Fragment offset not multiple of 8
 *         - Non-last fragment size not multiple of 8
 *         - Fragment too small (< 8 bytes)
 *         - Fragment extends beyond packet end
 *
 * @note Fragment offset and size must be multiples of 8 bytes per
 *       IPv4 (RFC791) and IPv6 (RFC8200) specifications
 *
 * @see RFC7915 Section 1.2 - Fragmentation and Reassembly
 */
static int
validate_fragment_params(
	uint16_t frag_offset,
	uint16_t frag_size,
	uint16_t total_len,
	bool more_fragments,
	bool is_icmp
) {
	// RFC7915 1.2: Fragmented ICMP/ICMPv6 packets will not be translated
	if (is_icmp) {
		LOG_DBG(NAT64, "Dropping fragmented ICMP packet\n");
		return -1;
	}

	// Fragment offset must be multiple of 8 bytes
	if (frag_offset % 8 != 0) {
		LOG_DBG(NAT64,
			"Invalid fragment offset (not multiple of 8): %u\n",
			frag_offset);
		return -1;
	}

	// Non-last fragments must be multiple of 8 bytes
	if (more_fragments && (frag_size % 8 != 0)) {
		LOG_DBG(NAT64,
			"Non-last fragment size not multiple of 8: %u\n",
			frag_size);
		return -1;
	}

	// Validate fragment size
	if (frag_size < 8) {
		LOG_DBG(NAT64, "Fragment too small: %u bytes\n", frag_size);
		return -1;
	}

	// Check for fragment overlap
	if (frag_offset + frag_size > total_len) {
		LOG_DBG(NAT64,
			"Fragment extends beyond packet end:\n"
			"  - Offset: %u\n"
			"  - Size: %u\n"
			"  - Total length: %u\n",
			frag_offset,
			frag_size,
			total_len);
		return -1;
	}

	return 0;
}

/**
 * @brief Translates ICMPv6 messages to ICMPv4 according to RFC7915 section 5.2
 *
 * This function implements the stateless translation of ICMPv6 messages to
 * ICMPv4 as specified in RFC7915. It handles various ICMPv6 message types
 * including:
 * - Echo Request/Reply (section 5.2.1)
 * - Destination Unreachable (section 5.2.2)
 * - Packet Too Big (section 5.2.3)
 * - Time Exceeded (section 5.2.4)
 * - Parameter Problem (section 5.2.5)
 *
 *
 * For error messages, the function also translates the embedded original packet
 * headers according to section 5.3. This includes adjusting MTU values,
 * translating IP headers, and recalculating checksums.
 *
 * @param nat64_config Pointer to NAT64 module configuration
 * @param packet Pointer to the packet structure containing the ICMPv6 header
 * @param new_ipv4_header Pointer to the new IPv4 header being constructed
 * @param prefix NAT64 prefix used for address translation
 * @param ip4 Pointer to IPv4 address for translation
 *
 * @return 0 on successful translation, -1 on failure (unsupported message type,
 *         invalid format, or other error conditions)
 *
 * @note RFC7915, section 5.2 - ICMPv6-to-ICMPv4 Translation
 */
static inline int
icmp_v6_to_v4(
	struct nat64_module_config *nat64_config,
	struct packet *packet,
	struct rte_ipv4_hdr *new_ipv4_header,
	uint8_t *ip4
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (!mbuf) {
		RTE_LOG(ERR, NAT64, "Failed to get mbuf from packet\n");
		return -1;
	}

	struct icmp6_hdr *icmp_header = rte_pktmbuf_mtod_offset(
		mbuf, struct icmp6_hdr *, packet->transport_header.offset
	);

	if (!icmp_header) {
		RTE_LOG(ERR, NAT64, "Failed to get ICMPv6 header from mbuf\n");
		return -1;
	}

	uint8_t type = icmp_header->icmp6_type;
	uint8_t code = icmp_header->icmp6_code;

	LOG_DBG(NAT64,
		"start translate ICMPv6 type: %d, code: %d \n",
		type,
		code);
	switch (type) {
	case ICMP6_ECHO_REQUEST:
		type = ICMP_ECHO;
		code = 0;
		break;

	case ICMP6_ECHO_REPLY:
		type = ICMP_ECHOREPLY;
		code = 0;
		break;

	case ICMP6_DST_UNREACH:
		type = ICMP_DEST_UNREACH;

		switch (code) {
		case ICMP6_DST_UNREACH_NOROUTE:
		case ICMP6_DST_UNREACH_BEYONDSCOPE:
		case ICMP6_DST_UNREACH_ADDR:
			code = ICMP_HOST_UNREACH;
			break;

		case ICMP6_DST_UNREACH_ADMIN:
			code = ICMP_HOST_ANO;
			break;

		case ICMP6_DST_UNREACH_NOPORT:
			code = ICMP_PORT_UNREACH;
			break;

		default:
			return -1;
		}
		break;

	case ICMP6_PACKET_TOO_BIG:
		type = ICMP_DEST_UNREACH;
		code = ICMP_FRAG_NEEDED;

		// MTU adjustment according to RFC7915 section 5.2
		uint32_t mtu = rte_be_to_cpu_32(icmp_header->icmp6_mtu);
		LOG_DBG(NAT64, "Original ICMPv6 MTU: %u\n", mtu);

		// RFC7915 Section 5.2: Handle MTU adjustment for Packet Too Big
		// messages Translate to an ICMPv4 Destination Unreachable (Type
		// 3) with Code 4, and adjust the ICMPv4 checksum both to take
		// the type change into account and to exclude the ICMPv6
		// pseudo-header.  The MTU field MUST be adjusted for the
		// difference between the IPv4 and IPv6 header sizes, taking
		// into account whether or not the packet in error includes a
		// Fragment Header, i.e., minimum((MTU value in the Packet Too
		// Big Message)-20, MTU_of_IPv4_nexthop,
		// (MTU_of_IPv6_nexthop)-20).
		if (mtu == 0) {
			// Router doesn't implement RFC1191, use cfg
			mtu = nat64_config->mtu.ipv4;
		}

		// Calculate header size difference
		uint16_t delta = sizeof(struct rte_ipv6_hdr) -
				 sizeof(struct rte_ipv4_hdr);

		// RFC7915: Adjust MTU for header size difference
		uint16_t adjusted_mtu = mtu - delta;

		if (nat64_config->mtu.ipv6 > 0) {
			adjusted_mtu =
				RTE_MIN(adjusted_mtu,
					nat64_config->mtu.ipv6 - delta);
		}

		if (nat64_config->mtu.ipv4 > 0) {
			// Account for IPv4->IPv6 translation overhead
			adjusted_mtu =
				RTE_MIN(adjusted_mtu, nat64_config->mtu.ipv4);
		}

		LOG_DBG(NAT64,
			"MTU adjustment:\n"
			"  - Original MTU: %u\n"
			"  - Config IPv6 MTU: %u\n"
			"  - Config IPv4 MTU: %u\n"
			"  - Adjusted MTU: %u\n",
			mtu,
			nat64_config->mtu.ipv6,
			nat64_config->mtu.ipv4,
			adjusted_mtu);

		mtu = adjusted_mtu;

		LOG_DBG(NAT64, "Adjusted ICMPv4 MTU: %u\n", mtu);

		// Store the adjusted MTU in the ICMPv4 header
		struct icmp *icmp_hdr = (struct icmp *)icmp_header;
		icmp_hdr->icmp_nextmtu = rte_cpu_to_be_16(mtu);
		break;

	case ICMP6_TIME_EXCEEDED:
		type = ICMP_TIME_EXCEEDED;
		// Code is unchanged
		break;

	case ICMP6_PARAM_PROB:
		switch (code) {
		case ICMP6_PARAMPROB_HEADER:
			type = ICMP_PARAMPROB;
			code = 0; // RFC7915: Set Code to 0 (Erroneous header
				  // field encountered)

			// RFC7915 Figure 6: Pointer translation from IPv6 to
			// IPv4
			uint32_t ptr =
				rte_be_to_cpu_32(icmp_header->icmp6_pptr);

			LOG_DBG(NAT64,
				"Translating ICMPv6 Parameter Problem pointer: "
				"%u\n",
				ptr);

			switch (ptr) {
			case 0: // Version/Traffic Class
				LOG_DBG(NAT64,
					"IPv6 Version/Traffic Class -> IPv4 "
					"Version/IHL\n");
				ptr = 0;
				break;
			case 1: // Traffic Class/Flow Label
				LOG_DBG(NAT64,
					"IPv6 Traffic Class/Flow Label -> IPv4 "
					"Type Of Service\n");
				ptr = 1;
				break;
			case 4: // Payload Length
			case 5:
				LOG_DBG(NAT64,
					"IPv6 Payload Length -> IPv4 Total "
					"Length\n");
				ptr = 2;
				break;
			case 6: // Next Header
				LOG_DBG(NAT64,
					"IPv6 Next Header -> IPv4 Protocol\n");
				ptr = 9;
				break;
			case 7: // Hop Limit
				LOG_DBG(NAT64,
					"IPv6 Hop Limit -> IPv4 Time to Live\n"
				);
				ptr = 8;
				break;
			case 8: // Source Address (first byte)
			case 9:
			case 10:
			case 11:
			case 12:
			case 13:
			case 14:
			case 15:
			case 16:
			case 17:
			case 18:
			case 19:
			case 20:
			case 21:
			case 22:
			case 23:
				LOG_DBG(NAT64,
					"IPv6 Source Address -> IPv4 Source "
					"Address\n");
				ptr = 12;
				break;
			case 24: // Destination Address (first byte)
			case 25:
			case 26:
			case 27:
			case 28:
			case 29:
			case 30:
			case 31:
			case 32:
			case 33:
			case 34:
			case 35:
			case 36:
			case 37:
			case 38:
			case 39:
				LOG_DBG(NAT64,
					"IPv6 Destination Address -> IPv4 "
					"Destination Address\n");
				ptr = 16;
				break;
			case 2: // Flow Label
			case 3:
				LOG_DBG(NAT64,
					"IPv6 Flow Label has no IPv4 "
					"equivalent, dropping packet\n");
				return -1;
			case 40: // Extension Headers
			default:
				LOG_DBG(NAT64,
					"Parameter Problem pointer not "
					"translatable: %u\n",
					ptr);
				return -1;
			}

			LOG_DBG(NAT64,
				"Translated Parameter Problem pointer to IPv4 "
				"offset: %u\n",
				ptr);
			icmp_header->icmp6_pptr = rte_cpu_to_be_32(
				ptr << 24
			); // set first byte to ptr and zeroed other bytes
			   // (reserved 6-8). TODO: support RFC4884.
			break;

		case ICMP6_PARAMPROB_NEXTHEADER:
			// RFC7915: Translate to Protocol Unreachable
			type = ICMP_DEST_UNREACH;
			code = ICMP_PROT_UNREACH;
			LOG_DBG(NAT64,
				"Next Header Problem -> Protocol Unreachable\n"
			);
			break;

		case ICMP6_PARAMPROB_OPTION:
			// RFC7915: Silently drop packets with unrecognized IPv6
			// options
			LOG_DBG(NAT64,
				"Dropping packet with unrecognized IPv6 "
				"option\n");
			return -1;

		default:
			LOG_DBG(NAT64,
				"Unknown Parameter Problem code: %u\n",
				code);
			return -1;
		}
		break;

	// Handle other ICMPv6 message types
	case MLD_LISTENER_QUERY:
	case MLD_LISTENER_REPORT:
	case MLD_LISTENER_REDUCTION:
	case ND_ROUTER_SOLICIT:
	case ND_ROUTER_ADVERT:
	case ND_NEIGHBOR_SOLICIT:
	case ND_NEIGHBOR_ADVERT:
	case ND_REDIRECT:
		// Single-hop message, silently drop
		LOG_DBG(NAT64,
			"Single-hop ICMPv6 message (type %d), dropping\n",
			type);
		return -1;

	// RFC7915 Section 4.2: Information Request/Reply (Type 15 and Type 16):
	// Obsoleted in ICMPv6. Silently drop.
	case ICMP6_ROUTER_RENUMBERING:
		LOG_DBG(NAT64,
			"Router Renumbering message (type %d), dropping\n",
			type);
		return -1;

		// RFC7915 Section 4.2: Timestamp and Timestamp Reply (Type 13
		// and Type 14): Obsoleted in ICMPv6. Silently drop. RFC7915
		// Section 4.2: Address Mask Request/Reply (Type 17 and Type
		// 18): Obsoleted in ICMPv6. Silently drop. RFC7915 Section 4.2:
		// Information Request/Reply (Type 15 and Type 16): Obsoleted in
		// ICMPv6. Silently drop.

	default:
		LOG_DBG(NAT64,
			"Unknown ICMPv6 message type: %d, dropping\n",
			type);
		return -1;
	}

	LOG_DBG(NAT64, "translate ICMP type: %d, code: %d \n", type, code);

	// Update the ICMP header with the translated type and code
	icmp_header->icmp6_type = type;
	icmp_header->icmp6_code = code;

	// RFC7915 Section 4.3: Handle ICMP error message translation
	bool is_error =
		(type == ICMP_DEST_UNREACH || type == ICMP_TIME_EXCEEDED ||
		 type == ICMP_PARAMPROB);

	if (is_error) {
		LOG_DBG(NAT64,
			"Translating ICMP error message with embedded packet\n"
		);

		// Get offset to embedded packet
		uint16_t embedded_offset = packet->transport_header.offset +
					   sizeof(struct icmp6_hdr);

		// RFC7915: Validate minimum length requirements
		uint16_t remaining_len =
			rte_pktmbuf_data_len(mbuf) - embedded_offset;
		if (remaining_len < sizeof(struct rte_ipv6_hdr)) {
			LOG_DBG(NAT64,
				"ICMP error message too short (len=%u, "
				"min=%zu)\n",
				remaining_len,
				sizeof(struct rte_ipv6_hdr));
			return -1;
		}

		// Extract the embedded IPv6 header
		struct rte_ipv6_hdr *ipv6_payload_header =
			rte_pktmbuf_mtod_offset(
				mbuf, struct rte_ipv6_hdr *, embedded_offset
			);

		if (!ipv6_payload_header) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get embedded IPv6 header\n");
			return -1;
		}

		// RFC7915: Validate embedded packet length
		uint16_t embedded_total_len =
			rte_be_to_cpu_16(new_ipv4_header->total_length) -
			rte_ipv4_hdr_len(new_ipv4_header) -
			sizeof(struct icmp6_hdr);

		if (remaining_len < embedded_total_len) {
			LOG_DBG(NAT64,
				"Embedded packet length (%u) exceeds remaining "
				"space (%u)\n",
				embedded_total_len,
				remaining_len);
			return -1;
		}

		// RFC7915: Check for nested ICMP errors (not allowed)
		if (ipv6_payload_header->proto == IPPROTO_ICMPV6) {
			struct icmp6_hdr *embedded_icmp =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct icmp6_hdr *,
					embedded_offset +
						sizeof(struct rte_ipv6_hdr)
				);

			if (!embedded_icmp) {
				RTE_LOG(ERR,
					NAT64,
					"Failed to get embedded ICMPv6 header\n"
				);
				return -1;
			}

			if (embedded_icmp->icmp6_type < 128) { // Error message
				LOG_DBG(NAT64,
					"Nested ICMP error messages not "
					"allowed\n");
				return -1;
			}
		}

		LOG_DBG(NAT64,
			"Embedded IPv6 packet validation:\n"
			"  - Total length: %u\n"
			"  - Payload length: %u\n"
			"  - Protocol: %u\n"
			"  - Remaining space: %u\n",
			embedded_total_len,
			rte_be_to_cpu_16(ipv6_payload_header->payload_len),
			ipv6_payload_header->proto,
			remaining_len);

		// Check if the embedded packet is fragmented
		uint8_t is_fragmented = 0;
		uint8_t next_header = ipv6_payload_header->proto;
		uint8_t count_header = 0;
		uint16_t offset = sizeof(struct rte_ipv6_hdr);

		// Skip extension headers
		while (next_header == IPPROTO_HOPOPTS ||
		       next_header == IPPROTO_ROUTING ||
		       next_header == IPPROTO_DSTOPTS) {
			if (offset >= remaining_len) {
				RTE_LOG(ERR,
					NAT64,
					"Reached end of packet while "
					"validating embedded packet\n");
				return -1;
			}
			count_header++;
			/* RFC8200 Section 4.1: Hop-by-Hop Options header must
			 * appear immediately after the IPv6 header if present
			 */
			if (count_header > 1 &&
			    next_header == IPPROTO_HOPOPTS) {
				RTE_LOG(ERR,
					NAT64,
					"Malformed packet: Hop-by-Hop Options "
					"header must be first (found at "
					"position %d)\n",
					count_header);
				return -1;
			}

			/* RFC8200 Section 4.4: Each extension header should
			 * occur at most once, except for Destination Options
			 * which may occur twice:
			 * - Once before Routing header
			 * - Once before upper-layer header */
			if (count_header > 4) {
				RTE_LOG(ERR,
					NAT64,
					"Malformed packet: Too many extension "
					"headers (%d > %d)\n",
					count_header,
					4);
				return -1;
			}
			// Skip this extension header
			struct ipv6_ext_2byte *ext_hdr =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_ext_2byte *,
					packet->transport_header.offset +
						sizeof(struct icmp6_hdr) +
						offset
				);

			if (!ext_hdr) {
				RTE_LOG(ERR,
					NAT64,
					"Failed to get IPv6 extension header\n"
				);
				return -1;
			}

			next_header = ext_hdr->next_type;
			uint16_t new_offset = offset + (ext_hdr->size + 1) * 8;
			if (new_offset >= remaining_len) {
				RTE_LOG(ERR,
					NAT64,
					"Reached end of packet while "
					"validating embedded packet\n");
				return -1;
			}
			offset = new_offset;
		}

		if (next_header == IPPROTO_FRAGMENT) {
			is_fragmented = 1;
			// Skip the fragment header
			struct ipv6_ext_fragment *frag_hdr =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_ext_fragment *,
					packet->transport_header.offset +
						sizeof(struct icmp6_hdr) +
						offset
				);

			if (!frag_hdr) {
				RTE_LOG(ERR,
					NAT64,
					"Failed to get IPv6 fragment header\n");
				return -1;
			}

			next_header = frag_hdr->next_type;
			offset += sizeof(struct ipv6_ext_fragment);
		}
		// Calculate the delta for header size difference
		int16_t delta = offset - sizeof(struct rte_ipv4_hdr);

		if (delta < 0) {
			// IPv4 header is larger than IPv6 header, strange -
			// drop
			LOG_DBG(NAT64,
				"ICMPv6 payload IPv4 header is larger than "
				"IPv6 headers\n");
			return -1;
		}

		// Create the IPv4 header in place of the IPv6 header
		struct rte_ipv4_hdr *new_ipv4_payload_header =
			rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv4_hdr *,
				packet->transport_header.offset +
					sizeof(struct icmp6_hdr) + delta
			);

		if (!new_ipv4_payload_header) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get space for embedded IPv4 header\n"
			);
			return -1;
		}
		uint32_t src_addr;
		// TODO: check prefix?
		rte_memcpy(&src_addr, &ipv6_payload_header->src_addr[12], 4);

		// Translate the embedded IPv6 header to IPv4
		new_ipv4_payload_header->version_ihl = RTE_IPV4_VHL_DEF;
		new_ipv4_payload_header->type_of_service =
			(rte_be_to_cpu_32(ipv6_payload_header->vtc_flow) >> 20
			) &
			0xFF;

		// Set the total length
		uint16_t payload_length =
			rte_be_to_cpu_16(ipv6_payload_header->payload_len);
		new_ipv4_payload_header->total_length = rte_cpu_to_be_16(
			payload_length + sizeof(struct rte_ipv4_hdr)
		);

		// Set identification, flags, and fragment offset
		if (is_fragmented) {
			struct ipv6_ext_fragment *frag_hdr =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_ext_fragment *,
					packet->transport_header.offset +
						sizeof(struct icmp6_hdr) +
						sizeof(struct rte_ipv6_hdr)
				);

			new_ipv4_payload_header->packet_id =
				frag_hdr->identification;

			uint16_t frag_data =
				rte_be_to_cpu_16(frag_hdr->offset_flag);
			uint16_t frag_offset =
				(frag_data & RTE_IPV6_EHDR_FO_MASK) >>
				RTE_IPV6_EHDR_FO_SHIFT;
			uint16_t frag_flags = frag_data & RTE_IPV6_EHDR_MF_MASK;

			new_ipv4_payload_header->fragment_offset =
				rte_cpu_to_be_16(
					(frag_offset << 3) |
					(frag_flags ? RTE_IPV4_HDR_MF_FLAG : 0)
				);
		} else {
			new_ipv4_payload_header->packet_id = 0;
			new_ipv4_payload_header->fragment_offset = 0;
		}

		// Set TTL, protocol, and addresses
		new_ipv4_payload_header->time_to_live =
			ipv6_payload_header->hop_limits;
		new_ipv4_payload_header->next_proto_id = next_header;

		new_ipv4_payload_header->dst_addr = *((uint32_t *)ip4);
		new_ipv4_payload_header->src_addr = src_addr;

		// Calculate the IPv4 header checksum
		new_ipv4_payload_header->hdr_checksum = 0;

		// If the embedded packet contains a transport layer header,
		// translate it too
		if (!is_fragmented ||
		    (is_fragmented &&
		     (new_ipv4_payload_header->fragment_offset &
		      RTE_BE16(RTE_IPV4_HDR_OFFSET_MASK)) == 0)) {
			uint16_t transport_offset =
				packet->transport_header.offset +
				sizeof(struct icmp6_hdr) +
				sizeof(struct rte_ipv4_hdr) + delta;

			switch (new_ipv4_payload_header->next_proto_id) {
			case IPPROTO_ICMPV6: {
				// Embedded ICMPv6 header needs to be translated
				// to ICMPv4
				struct icmp6_hdr *embedded_icmp6 =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct icmp6_hdr *,
						transport_offset
					);

				if (!embedded_icmp6) {
					RTE_LOG(ERR,
						NAT64,
						"Failed to get embedded ICMPv6 "
						"header\n");
					return -1;
				}

				// Comprehensive translation of embedded ICMPv6
				// to ICMPv4
				switch (embedded_icmp6->icmp6_type) {
				case ICMP6_ECHO_REQUEST:
					embedded_icmp6->icmp6_type = ICMP_ECHO;
					break;

				case ICMP6_ECHO_REPLY:
					embedded_icmp6->icmp6_type =
						ICMP_ECHOREPLY;
					break;

				default:
					// Other ICMPv6 types not translatable
					LOG_DBG(NAT64,
						"Embedded ICMPv6 type not "
						"translatable: %d\n",
						embedded_icmp6->icmp6_type);
					return -1;
					break;
				}
				embedded_icmp6->icmp6_code = 0;

				// Update the protocol field in the IPv4 header
				new_ipv4_payload_header->next_proto_id =
					IPPROTO_ICMP;

				// Recalculate the ICMP checksum
				struct icmp *embedded_icmp4 =
					(struct icmp *)embedded_icmp6;
				embedded_icmp4->icmp_cksum = 0;

				embedded_icmp4->icmp_cksum = ~rte_raw_cksum(
					embedded_icmp4, payload_length
				);
				if (embedded_icmp4->icmp_cksum == 0) {
					embedded_icmp4->icmp_cksum = 0xffff;
				}
				break;
			}
			case IPPROTO_UDP: {
				// Recalculate UDP checksum
				struct rte_udp_hdr *udp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_udp_hdr *,
						transport_offset
					);

				if (!udp_hdr) {
					RTE_LOG(ERR,
						NAT64,
						"Failed to get embedded UDP "
						"header\n");
					return -1;
				}

				udp_hdr->dgram_cksum = 0;
				udp_hdr->dgram_cksum = rte_ipv4_udptcp_cksum(
					new_ipv4_payload_header, udp_hdr
				);
				break;
			}
			case IPPROTO_TCP: {
				// Recalculate TCP checksum
				struct rte_tcp_hdr *tcp_hdr =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_tcp_hdr *,
						transport_offset
					);

				if (!tcp_hdr) {
					RTE_LOG(ERR,
						NAT64,
						"Failed to get embedded TCP "
						"header\n");
					return -1;
				}

				tcp_hdr->cksum = 0;
				tcp_hdr->cksum = rte_ipv4_udptcp_cksum(
					new_ipv4_payload_header, tcp_hdr
				);
				break;
			}
			}
		}
		new_ipv4_payload_header->hdr_checksum =
			rte_ipv4_cksum(new_ipv4_payload_header);

		// IPv4 header is smaller than IPv6 header, need to move data
		char *src = rte_pktmbuf_mtod_offset(
			mbuf,
			char *,
			packet->transport_header.offset +
				sizeof(struct icmp6_hdr) + delta
		);

		char *dst = rte_pktmbuf_mtod_offset(
			mbuf,
			char *,
			packet->transport_header.offset +
				sizeof(struct icmp6_hdr)
		);

		ssize_t len = rte_pktmbuf_data_len(mbuf) -
			      (packet->transport_header.offset +
			       sizeof(struct icmp6_hdr) + delta);

		if (len < 0) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to calculate payload len (negative "
				"value %ld)\n",
				len);
			return -1;
		}
		memmove(dst, src, len);

		// Adjust the packet length
		if (rte_pktmbuf_trim(mbuf, delta) != 0) {
			RTE_LOG(ERR, NAT64, "Failed to trim mbuf\n");
			return -1;
		}
		new_ipv4_header->total_length = rte_cpu_to_be_16(
			rte_be_to_cpu_16(new_ipv4_header->total_length) - delta
		);
	}
	// RFC7915: Calculate ICMPv4 checksum
	struct icmp *icmp_hdr = (struct icmp *)icmp_header;
	icmp_hdr->icmp_cksum = 0;

	// Get the ICMP message length from IPv4 header
	uint16_t ipv4_total_len =
		rte_be_to_cpu_16(new_ipv4_header->total_length);
	uint16_t ipv4_hdr_len = rte_ipv4_hdr_len(new_ipv4_header);
	uint16_t icmp_len = ipv4_total_len - ipv4_hdr_len;

	LOG_DBG(NAT64,
		"Calculating ICMPv4 checksum:\n"
		"  - IPv4 total length: %u\n"
		"  - IPv4 header length: %u\n"
		"  - ICMP length: %u\n",
		ipv4_total_len,
		ipv4_hdr_len,
		icmp_len);

	// Calculate checksum over the entire ICMP message
	uint32_t cksum = rte_raw_cksum(icmp_hdr, icmp_len);

	// Reduce to 16 bits and complement
	cksum = ~__rte_raw_cksum_reduce(cksum);

	// RFC1624: Handle all-zeros case
	if (cksum == 0) {
		cksum = 0xffff;
	}

	icmp_hdr->icmp_cksum = (uint16_t)cksum;

	LOG_DBG(NAT64,
		"ICMPv4 checksum calculation complete:\n"
		"  - Final checksum: 0x%04X\n"
		"  - Message type: %u\n"
		"  - Message code: %u\n",
		rte_be_to_cpu_16(icmp_hdr->icmp_cksum),
		icmp_hdr->icmp_type,
		icmp_hdr->icmp_code);

	return 0;
}

/* Maximum number of IPv6 extension headers allowed (RFC 8200) */
#define MAX_IPV6_EXT_HEADERS 8
/* Maximum number of Destination Options headers allowed */
#define MAX_DSTOPTS_HEADERS 2

/* Bit flags to track seen extension headers */
#define SEEN_HOPOPTS 0x01
#define SEEN_ROUTING 0x02
#define SEEN_FRAGMENT 0x04
#define SEEN_DSTOPTS 0x08
#define SEEN_AH 0x10
#define SEEN_ESP 0x20

/**
 * @brief Processes IPv6 extension headers according to RFC7915
 *
 * This function processes IPv6 extension headers in order as specified by
 * RFC7915 section 5.1 and RFC8200 section 4.1. It handles:
 * - Hop-by-Hop Options Header (must be first if present)
 * - Routing Header (dropping deprecated Type 0)
 * - Fragment Header (extracting fragmentation info)
 * - Destination Options Header
 * - Authentication Header (dropping)
 * - Encapsulating Security Payload Header (dropping)
 *
 * The function updates packet offsets and extracts fragmentation information
 * needed for NAT64 translation.
 *
 * @param nat64_config Pointer to NAT64 module configuration.
 *                     Currently unused but kept for future extensions.
 * @param packet Pointer to packet structure containing header offsets.
 *               Will be updated with new transport header offset.
 * @param next_header [out] Pointer to store final next header type after
 *                    processing all extension headers.
 * @param is_fragmented [out] Pointer to store fragmentation status.
 *                     Set to 1 if packet has Fragment Header.
 * @param frag_offset [out] Pointer to store fragment offset in bytes.
 *                   Valid only if is_fragmented is 1.
 * @param frag_flags [out] Pointer to store fragment flags (M bit).
 *                  Valid only if is_fragmented is 1.
 * @param frag_id [out] Pointer to store fragment ID.
 *               Valid only if is_fragmented is 1.
 * @param ext_hdrs_len [out] Pointer to store total length of all extension
 *                    headers in bytes.
 *
 * @return 0 on successful processing,
 *         -1 on failure with the following possible reasons:
 *         - Failed to get mbuf or headers
 *         - Invalid extension header length
 *         - Type 0 Routing Header (deprecated)
 *         - IPsec headers (not translated)
 *         - Malformed extension headers
 *
 * @note Extension headers must be processed in order specified by RFC8200:
 *       1. Hop-by-Hop Options
 *       2. Destination Options (before Routing)
 *       3. Routing
 *       4. Fragment
 *       5. Authentication
 *       6. Encapsulating Security Payload
 *       7. Destination Options (before upper layer)
 *
 * @see RFC7915 Section 5.1 - Extension Header Processing
 * @see RFC8200 Section 4.1 - Extension Header Order
 */
static int
process_ipv6_extension_headers(
	struct nat64_module_config *nat64_config,
	struct packet *packet,
	uint8_t *next_header,
	uint8_t *is_fragmented,
	uint16_t *frag_offset,
	uint16_t *frag_flags,
	uint32_t *frag_id,
	uint16_t *ext_hdrs_len
) {
	(void)nat64_config;
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (!mbuf) {
		RTE_LOG(ERR, NAT64, "Failed to get mbuf from packet\n");
		return -1;
	}

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (!ipv6_hdr) {
		RTE_LOG(ERR, NAT64, "Failed to get IPv6 header from mbuf\n");
		return -1;
	}

	// Initialize values
	*next_header = ipv6_hdr->proto;
	*is_fragmented = 0;
	*frag_offset = 0;
	*frag_flags = 0;
	*frag_id = 0;
	*ext_hdrs_len = 0;

	// Start offset for extension headers
	uint16_t current_offset =
		packet->network_header.offset + sizeof(struct rte_ipv6_hdr);

	uint8_t seen_headers = 0;  // Bitmap of seen header types
	uint8_t dstopts_count = 0; // Count of Destination Options headers
	uint8_t count_header = 0;

	// RFC7915 Section 5.1: Process extension headers in order
	while (current_offset < rte_pktmbuf_data_len(mbuf)) {
		// Check if we've reached a non-extension header
		if (*next_header != IPPROTO_HOPOPTS &&
		    *next_header != IPPROTO_ROUTING &&
		    *next_header != IPPROTO_FRAGMENT &&
		    *next_header != IPPROTO_DSTOPTS &&
		    *next_header != IPPROTO_AH && // Authentication Header
		    *next_header !=
			    IPPROTO_ESP) { // Encapsulating Security Payload
			break;
		}
		count_header++;
		// Check if we've reached maximum headers
		if (count_header >= MAX_IPV6_EXT_HEADERS) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Too many extension headers "
				"(%d > %d)\n",
				count_header,
				MAX_IPV6_EXT_HEADERS);
			return -1;
		}

		// RFC8200 Section 4.1: Hop-by-Hop must be first if present
		if (count_header > 1 && *next_header == IPPROTO_HOPOPTS) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Hop-by-Hop Options header "
				"must be first (found at position %d)\n",
				count_header);
			return -1;
		}

		// Check for duplicate headers (except Destination Options)
		if (*next_header == IPPROTO_HOPOPTS &&
		    (seen_headers & SEEN_HOPOPTS)) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Duplicate Hop-by-Hop "
				"Options header\n");
			return -1;
		} else if (*next_header == IPPROTO_ROUTING &&
			   (seen_headers & SEEN_ROUTING)) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Duplicate Routing header\n");
			return -1;
		} else if (*next_header == IPPROTO_FRAGMENT &&
			   (seen_headers & SEEN_FRAGMENT)) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Duplicate Fragment header\n"
			);
			return -1;
		} else if (*next_header == IPPROTO_DSTOPTS) {
			if (dstopts_count >= MAX_DSTOPTS_HEADERS) {
				RTE_LOG(ERR,
					NAT64,
					"Malformed packet: Too many "
					"Destination Options headers (%d)\n",
					dstopts_count + 1);
				return -1;
			}
			dstopts_count++;
		} else if (*next_header == IPPROTO_AH &&
			   (seen_headers & SEEN_AH)) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Duplicate Authentication "
				"header\n");
			return -1;
		} else if (*next_header == IPPROTO_ESP &&
			   (seen_headers & SEEN_ESP)) {
			RTE_LOG(ERR,
				NAT64,
				"Malformed packet: Duplicate ESP header\n");
			return -1;
		}

		// Update seen headers bitmap
		switch (*next_header) {
		case IPPROTO_HOPOPTS:
			seen_headers |= SEEN_HOPOPTS;
			break;
		case IPPROTO_ROUTING:
			seen_headers |= SEEN_ROUTING;
			break;
		case IPPROTO_FRAGMENT:
			seen_headers |= SEEN_FRAGMENT;
			break;
		case IPPROTO_DSTOPTS:
			seen_headers |= SEEN_DSTOPTS;
			break;
		case IPPROTO_AH:
			seen_headers |= SEEN_AH;
			break;
		case IPPROTO_ESP:
			seen_headers |= SEEN_ESP;
			break;
		default:
			break;
		}

		// Get the extension header
		void *ext_hdr =
			rte_pktmbuf_mtod_offset(mbuf, void *, current_offset);
		if (!ext_hdr) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get IPv6 extension header at offset "
				"%u\n",
				current_offset);
			return -1;
		}

		LOG_DBG(NAT64,
			"Processing IPv6 extension header:\n"
			"  - Type: %u\n"
			"  - Offset: %u\n"
			"  - Current total length: %u\n",
			*next_header,
			current_offset,
			*ext_hdrs_len);

		// Process based on extension header type
		switch (*next_header) {
		case IPPROTO_HOPOPTS:
		case IPPROTO_DSTOPTS: {
			// RFC7915: Process Hop-by-Hop and Destination Options
			struct ipv6_ext_2byte *hdr =
				(struct ipv6_ext_2byte *)ext_hdr;
			uint8_t hdr_len = (hdr->size + 1) * 8;

			// Validate header length
			if (current_offset + hdr_len >
			    rte_pktmbuf_data_len(mbuf)) {
				LOG_DBG(NAT64,
					"Extension header exceeds packet "
					"bounds\n");
				return -1;
			}

			*next_header = hdr->next_type;
			current_offset += hdr_len;
			*ext_hdrs_len += hdr_len;

			LOG_DBG(NAT64,
				"Processed Options header:\n"
				"  - Length: %u bytes\n"
				"  - Next header: %u\n",
				hdr_len,
				hdr->next_type);
			break;
		}

		case IPPROTO_ROUTING: {
			// RFC7915: Handle Routing Header
			struct ipv6_ext_2byte *hdr =
				(struct ipv6_ext_2byte *)ext_hdr;

			// Check for Type 0 Routing Header (deprecated)
			if (((uint8_t *)hdr)[2] == 0) {
				LOG_DBG(NAT64,
					"Dropping packet with Type 0 Routing "
					"Header\n");
				return -1;
			}

			uint8_t hdr_len = (hdr->size + 1) * 8;

			// Validate header length
			if (current_offset + hdr_len >
			    rte_pktmbuf_data_len(mbuf)) {
				LOG_DBG(NAT64,
					"Routing header exceeds packet bounds\n"
				);
				return -1;
			}

			*next_header = hdr->next_type;
			current_offset += hdr_len;
			*ext_hdrs_len += hdr_len;

			LOG_DBG(NAT64,
				"Processed Routing header:\n"
				"  - Type: %u\n"
				"  - Length: %u bytes\n"
				"  - Next header: %u\n",
				((uint8_t *)hdr)[2],
				hdr_len,
				hdr->next_type);
			break;
		}

		case IPPROTO_FRAGMENT: {
			// RFC7915: Process Fragment Header
			struct ipv6_ext_fragment *frag_hdr =
				(struct ipv6_ext_fragment *)ext_hdr;

			// Validate fragment header
			if (current_offset + sizeof(*frag_hdr) >
			    rte_pktmbuf_data_len(mbuf)) {
				LOG_DBG(NAT64,
					"Fragment header exceeds packet "
					"bounds\n");
				return -1;
			}

			*is_fragmented = 1;
			*next_header = frag_hdr->next_type;

			// Extract fragment data using DPDK macros
			uint16_t frag_data =
				rte_be_to_cpu_16(frag_hdr->offset_flag);
			*frag_offset = RTE_IPV6_GET_FO(frag_data);
			*frag_flags = RTE_IPV6_GET_MF(frag_data);
			*frag_id = frag_hdr->identification;

			// RFC7915: Drop fragmented ICMP
			if (frag_hdr->next_type == IPPROTO_ICMPV6) {
				LOG_DBG(NAT64,
					"Dropping fragmented ICMPv6 packet\n");
				return -1;
			}

			// Validate fragment offset
			if (*frag_offset % 8) {
				LOG_DBG(NAT64,
					"Invalid fragment offset (not multiple "
					"of 8): %u\n",
					*frag_offset);
				return -1;
			}

			current_offset += sizeof(*frag_hdr);
			*ext_hdrs_len += sizeof(*frag_hdr);

			LOG_DBG(NAT64,
				"Processed Fragment header:\n"
				"  - Offset: %u\n"
				"  - More Fragments: %u\n"
				"  - ID: 0x%x\n"
				"  - Next header: %u\n",
				*frag_offset,
				*frag_flags,
				*frag_id,
				*next_header);
			break;
		}

		case IPPROTO_AH:
		case IPPROTO_ESP: {
			// RFC7915: IPsec headers are not translated
			LOG_DBG(NAT64,
				"Dropping packet with IPsec header (type %u)\n",
				*next_header);
			return -1;
		}

		default:
			RTE_LOG(ERR,
				NAT64,
				"Unexpected extension header type: %u\n",
				*next_header);
			return -1;
		}
	}

	if (current_offset >= rte_pktmbuf_data_len(mbuf)) {
		RTE_LOG(ERR, NAT64, "Extension header exceeds packet bounds\n");
		return -1;
	}

	// Update transport header offset to account for all extension headers
	packet->transport_header.offset = packet->network_header.offset +
					  sizeof(struct rte_ipv6_hdr) +
					  *ext_hdrs_len;

	LOG_DBG(NAT64,
		"Finished processing IPv6 extension headers: next_header=%u, "
		"is_fragmented=%u, ext_hdrs_len=%u\n",
		*next_header,
		*is_fragmented,
		*ext_hdrs_len);

	return 0;
}

/**
 * @brief Handles IPv6 to IPv4 packet translation according to RFC7915
 *
 * This function implements the core IPv6-to-IPv4 translation logic as specified
 * in RFC7915. It performs the following steps:
 * 1. Extracts and validates IPv6 header
 * 2. Looks up IPv4 address mapping for source IPv6 address
 * 3. Processes IPv6 extension headers (if any)
 * 4. Validates fragmentation parameters
 * 5. Translates IP headers and adjusts packet size
 * 6. Performs protocol-specific translations:
 *    - ICMPv6 to ICMPv4 (including embedded packet translation)
 *    - TCP/UDP checksum recalculation
 *
 * The function handles:
 * - Header translation and address mapping
 * - Extension header processing
 * - Fragmentation handling
 * - Protocol-specific translations (ICMP, TCP, UDP)
 * - Checksum recalculation
 *
 * @param nat64_config Pointer to NAT64 module configuration containing:
 *                     - Address mappings
 *                     - MTU settings
 *                     - NAT64 prefixes
 * @param packet Pointer to the packet structure containing:
 *               - Network header offset
 *               - Transport header offset
 *               - IPv6 header and payload
 *
 * @return 0 on successful translation,
 *         -1 on failure with the following possible reasons:
 *         - Failed to get packet headers
 *         - No matching IPv4 address mapping found
 *         - Invalid extension headers
 *         - Invalid fragmentation parameters
 *         - Unsupported protocol or message type
 *         - Memory allocation/buffer size issues
 *
 * @note The function modifies the packet in-place, adjusting offsets and
 *       recalculating checksums as needed.
 *
 * @see RFC7915 - IP/ICMP Translation Algorithm
 * @see process_ipv6_extension_headers() For extension header handling
 * @see icmp_v6_to_v4() For ICMPv6 translation
 */
static int
nat64_handle_v6(
	struct nat64_module_config *nat64_config, struct packet *packet
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (!mbuf) {
		RTE_LOG(ERR, NAT64, "Failed to get mbuf from packet\n");
		return -1;
	}

	struct rte_ipv6_hdr *ipv6_header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (!ipv6_header) {
		RTE_LOG(ERR, NAT64, "Failed to get IPv6 header from mbuf\n");
		return -1;
	}

	struct ip4to6 *new_src_addr =
		find_ip6to4(nat64_config, (uint8_t *)&ipv6_header->src_addr);
	if (NULL == new_src_addr) {
		LOG_DBG(NAT64,
			"not found mapping for " IPv6_BYTES_FMT ". Drop\n",
			IPv6_BYTES(ipv6_header->src_addr));
		return -1;
	}

	LOG_DBG(NAT64,
		"found mapping " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT "\n",
		IPv6_BYTES(ipv6_header->src_addr),
		IPv4_BYTES(RTE_BE32(new_src_addr->ip4)));

	// Process IPv6 extension headers and check for fragmentation
	uint8_t is_fragmented = 0;
	uint8_t next_header = ipv6_header->proto;
	uint16_t frag_offset = 0;
	uint16_t frag_flags = 0;
	uint32_t frag_id = 0;
	uint16_t ext_hdrs_len = 0;
	LOG_DBG(NAT64, "Processing IPv6 extension headers\n");
	// Process extension headers using helper function
	int ret = process_ipv6_extension_headers(
		nat64_config,
		packet,
		&next_header,
		&is_fragmented,
		&frag_offset,
		&frag_flags,
		&frag_id,
		&ext_hdrs_len
	);
	if (ret) {
		return ret;
	}

	// If packet is fragmented, validate fragment parameters
	if (is_fragmented) {
		uint16_t total_len = rte_be_to_cpu_16(ipv6_header->payload_len);
		uint16_t frag_size = total_len - ext_hdrs_len;

		if (validate_fragment_params(
			    frag_offset,
			    frag_size,
			    total_len,
			    frag_flags,
			    next_header == IPPROTO_ICMPV6
		    ) != 0) {
			return -1;
		}
	}

	// Update transport header offset to account for all extension headers
	packet->transport_header.offset = packet->network_header.offset +
					  sizeof(struct rte_ipv6_hdr) +
					  ext_hdrs_len;

	LOG_DBG(NAT64,
		"Finished processing IPv6 extension headers: next_header=%u, "
		"is_fragmented=%u, ext_hdrs_len=%u\n",
		next_header,
		is_fragmented,
		ext_hdrs_len);

	// Calculate size difference between headers
	uint16_t delta = packet->transport_header.offset -
			 packet->network_header.offset -
			 sizeof(struct rte_ipv4_hdr);

	LOG_DBG(NAT64,
		"MTU handling:\n"
		"  - Transport offset: %u\n"
		"  - Network offset: %u\n"
		"  - Header delta: %d\n"
		"  - IPv4 MTU: %u\n",
		packet->transport_header.offset,
		packet->network_header.offset,
		delta,
		(unsigned int)nat64_config->mtu.ipv4);

	struct rte_ipv4_hdr *new_ipv4_header = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_ipv4_hdr *,
		packet->network_header.offset + delta
	);

	if (!new_ipv4_header) {
		RTE_LOG(ERR, NAT64, "Failed to get new IPv4 header from mbuf\n"
		);
		return -1;
	}

	uint16_t payload_length = rte_be_to_cpu_16(ipv6_header->payload_len);

	new_ipv4_header->version_ihl = RTE_IPV4_VHL_DEF;
	new_ipv4_header->type_of_service =
		(rte_be_to_cpu_32(ipv6_header->vtc_flow) >> 20) & 0xFF;
	new_ipv4_header->total_length =
		rte_cpu_to_be_16(payload_length + sizeof(struct rte_ipv4_hdr));

	// Set packet ID and fragment offset if the packet is fragmented
	if (is_fragmented) {
		new_ipv4_header->packet_id = frag_id;
		new_ipv4_header->fragment_offset = rte_cpu_to_be_16(
			(frag_offset << 3) |
			(frag_flags ? RTE_IPV4_HDR_MF_FLAG : 0)
		);
	} else {
		new_ipv4_header->packet_id = 0;
		new_ipv4_header->fragment_offset = 0;
	}
	new_ipv4_header->time_to_live = ipv6_header->hop_limits;
	new_ipv4_header->next_proto_id = ipv6_header->proto;
	new_ipv4_header->hdr_checksum = 0;

	new_ipv4_header->src_addr = new_src_addr->ip4;

	if (ipv6_header->proto == IPPROTO_FRAGMENT) {
		rte_memcpy(
			&new_ipv4_header->dst_addr,
			&ipv6_header->dst_addr[12],
			sizeof(uint32_t)
		);
	}

	// handle ICMP, TCP, UDP
	if (ipv6_header->proto == IPPROTO_ICMPV6) {
		new_ipv4_header->next_proto_id = IPPROTO_ICMP;

		struct icmp6_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct icmp6_hdr *,
			packet->transport_header.offset
		);

		if (!icmp_header) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get ICMPv6 header from mbuf\n");
			return -1;
		}

		int result = icmp_v6_to_v4(
			nat64_config,
			packet,
			new_ipv4_header,
			(uint8_t *)&new_ipv4_header->src_addr
		);
		if (result) {
			LOG_DBG(NAT64,
				"ICMP translation failed, dropping packet\n");
			return -1;
		}
	} else if (ipv6_header->proto == IPPROTO_UDP) {
		// Recalculate UDP checksum for IPv4
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		if (!udp_hdr) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get UDP header from mbuf\n");
			return -1;
		}

		udp_hdr->dgram_cksum = 0;
		udp_hdr->dgram_cksum = rte_ipv4_udptcp_cksum_mbuf(
			mbuf, new_ipv4_header, packet->transport_header.offset
		);
		LOG_DBG(NAT64,
			"UDP checksum calculated: 0x%04X\n",
			rte_be_to_cpu_16(udp_hdr->dgram_cksum));

	} else if (ipv6_header->proto == IPPROTO_TCP) {
		// Recalculate TCP checksum for IPv4
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		if (!tcp_hdr) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get TCP header from mbuf\n");
			return -1;
		}

		tcp_hdr->cksum = 0;
		tcp_hdr->cksum = rte_ipv4_udptcp_cksum_mbuf(
			mbuf, new_ipv4_header, packet->transport_header.offset
		);
	}
	// copy l2 header
	rte_memcpy(
		rte_pktmbuf_mtod_offset(mbuf, char *, delta),
		rte_pktmbuf_mtod(mbuf, char *),
		packet->network_header.offset
	);

	// Calculate IPv4 header checksum
	new_ipv4_header->hdr_checksum = rte_ipv4_cksum(new_ipv4_header);

	// reduce packet
	if (rte_pktmbuf_adj(mbuf, delta) == NULL) {
		RTE_LOG(ERR, NAT64, "adjust mbuf failed. Delta: %d\n", delta);
		return -1;
	}

	// adjust new transport header offset
	packet->transport_header.offset =
		packet->network_header.offset + sizeof(struct rte_ipv4_hdr);

	// set ipv4 header type
	uint16_t *next_header_type = rte_pktmbuf_mtod_offset(
		mbuf, uint16_t *, packet->network_header.offset - 2
	);
	if (!next_header_type) {
		RTE_LOG(ERR, NAT64, "Failed to get next header type from mbuf\n"
		);
		return -1;
	}

	*next_header_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	return 0;
}

/**
 * @brief Copies and translates IPv4 header fields to IPv6 header.
 *
 * This function performs the translation of IPv4 header fields to their IPv6
 * equivalents according to RFC7915 requirements. It handles traffic class, flow
 * label, hop limit, and address translation.
 *
 * @param mbuf Pointer to the packet mbuf
 * @param ipv4_header Pointer to the IPv4 header
 * @param new_ipv6_header Pointer to the new IPv6 header
 * @param l3_off Layer 3 offset in the packet
 * @param prefix NAT64 prefix for address translation
 * @param ip6 IPv6 address for translation
 * @param is_fragmented Whether the packet is fragmented
 * @param delta Size difference between headers
 * @param swap_addr Whether to swap source and destination addresses
 * @return 0 on success, -1 on failure
 */
static inline int
copy_ipv4_to_ipv6_hdr(
	struct rte_mbuf *mbuf,
	struct rte_ipv4_hdr *ipv4_header,
	struct rte_ipv6_hdr *new_ipv6_header,
	uint16_t l3_off,
	uint8_t *prefix,
	uint8_t *ip6,
	uint8_t is_fragmented,
	int16_t delta, // theoretically may be less than zero
	bool swap_addr
) {
	new_ipv6_header->vtc_flow = rte_cpu_to_be_32(
		(6 << 28) |
		(ipv4_header->type_of_service << RTE_IPV6_HDR_TC_SHIFT)
	);

	// save transport payload length. - ip header len.
	uint16_t payload_len =
		rte_be_to_cpu_16(ipv4_header->total_length) - delta;

	new_ipv6_header->payload_len = rte_cpu_to_be_16(payload_len);

	new_ipv6_header->hop_limits = ipv4_header->time_to_live;
	new_ipv6_header->proto = ipv4_header->next_proto_id;
	uint32_t dst_addr = ipv4_header->dst_addr; // may be overwrite bellow

	if (is_fragmented) {
		// RFC7915: Handle IPv4 fragments
		uint16_t frag_offset =
			rte_be_to_cpu_16(ipv4_header->fragment_offset);
		uint16_t more_fragments =
			!!(frag_offset & RTE_IPV4_HDR_MF_FLAG);
		uint16_t offset_value = (frag_offset & RTE_IPV4_HDR_OFFSET_MASK)
					<< 3;

		LOG_DBG(NAT64,
			"IPv4 fragment: offset=%u, more_fragments=%u, "
			"id=0x%x\n",
			offset_value,
			more_fragments,
			rte_be_to_cpu_16(ipv4_header->packet_id));

		// RFC7915: Fragment offset must be a multiple of 8
		if (offset_value % 8) {
			LOG_DBG(NAT64,
				"Invalid IPv4 fragment offset (not multiple of "
				"8): %u\n",
				offset_value);
			return -1;
		}

		// Set IPv6 header next protocol to Fragment
		new_ipv6_header->proto = IPPROTO_FRAGMENT;

		// Create IPv6 fragment header
		struct rte_ipv6_fragment_ext *frag_hdr =
			rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv6_fragment_ext *,
				l3_off + sizeof(struct rte_ipv6_hdr)
			);

		if (!frag_hdr) {
			LOG_DBG(NAT64,
				"Failed to get space for IPv6 fragment header\n"
			);
			return -1;
		}

		// Set fragment header fields
		frag_hdr->next_header =
			ipv4_header->next_proto_id == IPPROTO_ICMP
				? IPPROTO_ICMPV6
				: ipv4_header->next_proto_id;
		frag_hdr->reserved = 0;

		// Set fragment offset and M flag using DPDK macros
		frag_hdr->frag_data = rte_cpu_to_be_16(RTE_IPV6_SET_FRAG_DATA(
			offset_value >> 3, // Convert offset to units of 8 bytes
			more_fragments
		));

		// Copy IPv4 ID to IPv6 fragment ID and ensure proper byte order
		frag_hdr->id =
			rte_cpu_to_be_32(rte_be_to_cpu_16(ipv4_header->packet_id
			));

		LOG_DBG(NAT64,
			"Created IPv6 fragment header: next_header=%u, "
			"offset=%u, M=%u, id=0x%x\n",
			frag_hdr->next_header,
			offset_value,
			more_fragments,
			rte_be_to_cpu_32(frag_hdr->id));
	}

	if (swap_addr) {
		SET_IPV4_MAPPED_IPV6(
			&new_ipv6_header->dst_addr, prefix, &dst_addr
		);
		rte_memcpy(
			&new_ipv6_header->src_addr, ip6, 16 * sizeof(uint8_t)
		);
	} else {
		// bellow we write in original ip4 header memory add
		SET_IPV4_MAPPED_IPV6(
			&new_ipv6_header->src_addr,
			prefix,
			&ipv4_header->src_addr
		);
		rte_memcpy(
			&new_ipv6_header->dst_addr, ip6, 16 * sizeof(uint8_t)
		);
	}

	return 0;
}

/**
 * @brief Translates ICMPv4 to ICMPv6
 *
 * https://datatracker.ietf.org/doc/html/rfc7915#section-4.2
 *
 * @param nat64_config Pointer to the NAT64 module configuration.
 * @param packet Pointer to the packet being translated.
 * @param new_ipv6_header Pointer to the new IPv6 header.
 * @param prefix Pointer to the NAT64 IPv6 prefix.
 * @param ip6 Pointer to the translated IPv6 address.
 * @return 0 on successful translation, -1 on failure.
 */
static inline int
icmp_v4_to_v6(
	struct nat64_module_config *nat64_config,
	struct packet *packet,
	struct rte_ipv6_hdr *new_ipv6_header,
	uint8_t *prefix,
	uint8_t *ip6
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct icmp *icmp_header = rte_pktmbuf_mtod_offset(
		mbuf, struct icmp *, packet->transport_header.offset
	);

	uint8_t type = icmp_header->icmp_type;
	uint8_t code = icmp_header->icmp_code;

	LOG_DBG(NAT64,
		"start translation ICMPv4 type: %d, "
		"code: %d\n",
		type,
		code);

	switch (type) {
	case ICMP_UNREACH:
		/*
		 * ICMPv4 error messages:
		 *
		 * Destination Unreachable (Type 3): Translate the Code as
		 * described below, set the Type to 1, and adjust the ICMP
		 * checksum both to take the type/code change into account and
		 * to include the ICMPv6 pseudo-header.
		 *
		 * Translate the Code as follows:
		 *
		 * Code 0, 1 (Net Unreachable, Host Unreachable): Set the Code
		 * to 0 (No route to destination).
		 *
		 * Code 2 (Protocol Unreachable): Translate to an ICMPv6
		 * Parameter Problem (Type 4, Code 1) and make the Pointer point
		 * to the IPv6 Next Header field.
		 *
		 * Code 3 (Port Unreachable): Set the Code to 4 (Port
		 * unreachable).
		 *
		 * Code 4 (Fragmentation Needed and DF was Set): Translate to an
		 * ICMPv6 Packet Too Big message (Type 2) with Code set to 0.
		 * The MTU field MUST be adjusted for the difference between the
		 * IPv4 and IPv6 header sizes, but MUST NOT be set to a value
		 * smaller than the minimum IPv6 MTU (1280 bytes). That is, it
		 * should be set to
		 *
		 * maximum(1280, minimum((MTU value in the Packet Too Big
		 * Message) + 20, MTU_of_IPv6_nexthop, (MTU_of_IPv4_nexthop) +
		 * 20)).
		 *
		 * Note that if the IPv4 router set the MTU field to zero, i.e.,
		 * the router does not implement [RFC1191], then the translator
		 * MUST use the plateau values specified in [RFC1191] to
		 * determine a likely path MTU and include that path MTU in the
		 * ICMPv6 packet. (Use the greatest plateau value that is less
		 * than the returned Total Length field, but that is larger than
		 * or equal to 1280.)
		 *
		 * See also the requirements in Section 7.
		 *
		 * Code 5 (Source Route Failed): Set the Code to 0 (No route to
		 * destination). Note that this error is unlikely since source
		 * routes are not translated.
		 *
		 * Code 6, 7, 8: Set the Code to 0 (No route to destination).
		 *
		 * Code 9, 10 (Communication with Destination Host
		 * Administratively Prohibited): Set the Code to 1
		 * (Communication with destination administratively prohibited).
		 *
		 * Code 11, 12: Set the Code to 0 (No route to destination).
		 *
		 * Code 13 (Communication Administratively Prohibited): Set the
		 * Code to 1 (Communication with destination administratively
		 * prohibited).
		 *
		 * Code 14 (Host Precedence Violation): Silently drop.
		 *
		 * Code 15 (Precedence cutoff in effect): Set the Code to 1
		 * (Communication with destination administratively prohibited).
		 *
		 * Other Code values: Silently drop.
		 */

		type = ICMP6_DST_UNREACH;

		switch (code) {
		case ICMP_HOST_UNREACH:
		case ICMP_NET_UNREACH:
		case ICMP_SR_FAILED:
		case ICMP_NET_UNKNOWN:
		case ICMP_HOST_UNKNOWN:
		case ICMP_HOST_ISOLATED:
		case ICMP_NET_UNR_TOS:
		case ICMP_HOST_UNR_TOS:
			code = ICMP6_DST_UNREACH_NOROUTE;
			break;

		case ICMP_NET_ANO:
		case ICMP_HOST_ANO:
		case ICMP_PKT_FILTERED:
		case ICMP_PREC_CUTOFF:
			code = ICMP6_DST_UNREACH_ADMIN;
			break;

		case ICMP_PROT_UNREACH:
			type = ICMP6_PARAM_PROB;
			code = ICMP6_PARAMPROB_NEXTHEADER;
			((struct icmp6_hdr *)icmp_header)->icmp6_pptr =
				rte_be_to_cpu_32(6);
			break;

		case ICMP_PORT_UNREACH:
			code = ICMP6_DST_UNREACH_NOPORT;
			break;

		case ICMP_FRAG_NEEDED:
			type = ICMP6_PACKET_TOO_BIG;
			code = 0;

			// Get MTU from ICMP header
			uint16_t mtu =
				rte_be_to_cpu_16(icmp_header->icmp_nextmtu);

			// RFC7915: If MTU is 0, router doesn't implement
			// RFC1191
			if (mtu == 0) {
				// TODO: RFC1191 values
				// Use configured MTU
				mtu = nat64_config->mtu.ipv4;
			}
			mtu += 20;

			// Apply configured MTU limits if set
			if (nat64_config->mtu.ipv6 > 0) {
				mtu = RTE_MIN(mtu, nat64_config->mtu.ipv6);
			}
			if (nat64_config->mtu.ipv4 > 0) {
				mtu = RTE_MIN(mtu, nat64_config->mtu.ipv4 + 20);
			}
			// RFC7915: MTU must not be less than IPv6 minimum
			// (1280)
			mtu = RTE_MAX(1280, mtu);

			LOG_DBG(NAT64,
				"MTU translation:\n"
				"  - Original MTU: %u\n"
				"  - Adjusted MTU: %u\n"
				"  - Config IPv6 MTU: %u\n",
				rte_be_to_cpu_16(icmp_header->icmp_nextmtu),
				mtu,
				nat64_config->mtu.ipv6);

			icmp_header->icmp_nextmtu = rte_cpu_to_be_32(mtu);
			break;

		default:
			return -1; // other - drop
			break;
		}

		break;

	case ICMP_ECHO:
		type = ICMP6_ECHO_REQUEST;
		code = 0;
		break;

	case ICMP_ECHOREPLY:
		type = ICMP6_ECHO_REPLY;
		code = 0;
		break;

	case ICMP_TIME_EXCEEDED:
		type = ICMP6_TIME_EXCEEDED;
		break;

	case ICMP_PARAMPROB:
		/*
		 * Translate the Code as follows:
		 *
		 * Code 0 (Pointer indicates the error): Set the Code to 0
		 *     (Erroneous header field encountered) and update the
		 *     pointer as defined in Figure 3. (If the Original IPv4
		 *     Pointer Value is not listed or the Translated IPv6
		 *     Pointer Value is listed as "n/a", silently drop the
		 *     packet.)
		 *
		 * Code 1 (Missing a required option): Silently drop.
		 *
		 * Code 2 (Bad length): Set the Code to 0 (Erroneous header
		 *     field encountered) and update the pointer as defined in
		 *     Figure 3. (If the Original IPv4 Pointer Value is not
		 *     listed or the Translated IPv6 Pointer Value is listed as
		 *     "n/a", silently drop the packet.)
		 *
		 * Other Code values: Silently drop.
		 */
		if (code != 0 && code != 2) {
			return -1; // drop for all other code values
		}
		type = ICMP6_PARAM_PROB;
		code = ICMP6_PARAMPROB_HEADER;

		/*
		 // clang-format off
		 * +--------------------------------+--------------------------------+
		 * |   Original IPv4 Pointer Value  | Translated IPv6 Pointer
		 Value  |
		 * +--------------------------------+--------------------------------+
		 * |  0  | Version/IHL              |  0  | Version/Traffic
		 Class    |
		 * |  1  | Type Of Service          |  1  | Traffic Class/Flow
		 Label |
		 * | 2,3 | Total Length             |  4  | Payload Length |
		 * | 4,5 | Identification           | n/a | |
		 * |  6  | Flags/Fragment Offset    | n/a | |
		 * |  7  | Fragment Offset          | n/a | |
		 * |  8  | Time to Live             |  7  | Hop Limit |
		 * |  9  | Protocol                 |  6  | Next Header |
		 * |10,11| Header Checksum          | n/a | |
		 * |12-15| Source Address           |  8  | Source Address |
		 * |16-19| Destination Address      | 24  | Destination Address
		 |
		 * +--------------------------------+--------------------------------+
		 // clang-format on
		 * Figure 3: Pointer Value for Translating from IPv4 to IPv6
		 */

		uint8_t ptr = icmp_header->icmp_pptr;
		switch (ptr) {
		case 0:
		case 1:
			// clang-format off
			// |  0  | Version/IHL              |  0  | Version/Traffic Class    |
			// |  1  | Type Of Service          |  1  | Traffic Class/Flow Label |
			// clang-format on
			break;
		case 2:
		case 3:
			// clang-format off
			// | 2,3 | Total Length             |  4  | Payload Length           |
			// clang-format on
			ptr = 4;
			break;
		case 8:
			// clang-format off
			// |  8  | Time to Live             |  7  | Hop Limit                |
			// clang-format on
			ptr = 7;
			break;
		case 9:
			// clang-format off
			// |  9  | Protocol                 |  6  | Next Header              |
			// clang-format on
			ptr = 6;
			break;
		case 12:
		case 13:
		case 14:
		case 15:
			// clang-format off
			// |12-15| Source Address           |  8  | Source Address           |
			// clang-format on
			ptr = 8;
			break;
		case 16:
		case 17:
		case 18:
		case 19:
			// clang-format off
			// |16-19| Destination Address      | 24  | Destination Address      |
			// clang-format on
			ptr = 24;
			break;

		default:
			// drop for all other pointer values as they are not
			// listed in Figure 3
			return -1;
			break;
		}

		((struct icmp6_hdr *)icmp_header)->icmp6_pptr =
			rte_be_to_cpu_32(ptr);
		break;

	default:
		LOG_DBG(NAT64,
			"not translatable ICMPv4 type: %d, code: %d \n",
			type,
			code);
		return -1;
		break;
	}

	LOG_DBG(NAT64, "translated ICMP type: %d, code: %d \n", type, code);

	icmp_header->icmp_type = type;
	icmp_header->icmp_code = code;

	// translate rest if error type
	if (type < 128) {
		struct rte_ipv4_hdr *ipv4_payload_header =
			rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_ipv4_hdr *,
				packet->transport_header.offset +
					sizeof(struct rte_icmp_hdr)
			);
		int16_t delta = sizeof(struct rte_ipv6_hdr) -
				rte_ipv4_hdr_len(ipv4_payload_header);

		uint8_t is_fragmented =
			ipv4_payload_header->fragment_offset &
			RTE_BE16(
				RTE_IPV4_HDR_MF_FLAG | RTE_IPV4_HDR_OFFSET_MASK
			);

		if (is_fragmented) {
			delta += RTE_IPV6_FRAG_HDR_SIZE;
		}

		if (delta < 0) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to translate icmp payload with ipv4 "
				"header "
				"with options\n");
			return -1;
		}

		uint16_t payload_len =
			rte_be_to_cpu_16(new_ipv6_header->payload_len);
		uint16_t new_payload_len = payload_len + delta;
		int16_t mtu_overflow =
			nat64_config->mtu.ipv6 -
			(packet->transport_header.offset + new_payload_len -
			 (new_ipv6_header->proto == IPPROTO_FRAGMENT
				  ? RTE_IPV6_FRAG_HDR_SIZE
				  : 0));
		if (mtu_overflow < 0) {
			new_payload_len += mtu_overflow;
		}
		// adjust payload length
		new_ipv6_header->payload_len =
			rte_cpu_to_be_16(new_payload_len);

		uint16_t buff_delta =
			delta + (mtu_overflow < 0 ? mtu_overflow : 0
				); // no sense append oveflow

		if (buff_delta > 0) {
			if (!rte_pktmbuf_append(mbuf, buff_delta)) {
				RTE_LOG(ERR,
					NAT64,
					"Failed to append mbuf for icmpv6 "
					"payload\n");
				return -1;
			}
		} else {
			if (rte_pktmbuf_trim(mbuf, -buff_delta)) {
				RTE_LOG(ERR,
					NAT64,
					"Failed to trim mbuf for icmpv6 "
					"payload\n");
				return -1;
			}
		}

		// move(because overlap) icmp payload
		memmove(rte_pktmbuf_mtod_offset(
				mbuf,
				void *,
				packet->transport_header.offset +
					sizeof(struct rte_icmp_hdr)
			) + delta,
			rte_pktmbuf_mtod_offset(
				mbuf,
				void *,
				packet->transport_header.offset +
					sizeof(struct rte_icmp_hdr)
			),
			new_payload_len - sizeof(struct rte_icmp_hdr));

		// new ipv6 payload header at old place
		struct rte_ipv6_hdr *new_ipv6_payload_header =
			(struct rte_ipv6_hdr *)ipv4_payload_header;
		// new place for ipv4 payload header
		ipv4_payload_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->transport_header.offset +
				sizeof(struct rte_icmp_hdr) + delta
		);
		uint8_t skip_translate = ipv4_payload_header->fragment_offset &
					 RTE_BE16(RTE_IPV4_HDR_OFFSET_MASK);
		if (copy_ipv4_to_ipv6_hdr(
			    mbuf,
			    ipv4_payload_header,
			    new_ipv6_payload_header,
			    packet->transport_header.offset +
				    sizeof(struct rte_icmp_hdr),
			    prefix,
			    ip6,
			    is_fragmented,
			    delta,
			    true
		    )) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to copy icmp payload ipv4 to ipv6 "
				"header\n");
			return -1;
		}
		if (!skip_translate) {
			// no frag or first fragment
			uint16_t payload_offset =
				packet->transport_header.offset +
				sizeof(struct rte_icmp_hdr) +
				sizeof(struct rte_ipv6_hdr) +
				(is_fragmented ? RTE_IPV6_FRAG_HDR_SIZE : 0);
			switch (new_ipv6_payload_header->proto) {
			case IPPROTO_ICMP:
				new_ipv6_payload_header->proto = IPPROTO_ICMPV6;

				struct rte_icmp_hdr *icmp_header_payload =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_icmp_hdr *,
						payload_offset
					);
				if (icmp_header_payload->icmp_type ==
				    ICMP_ECHO) {
					icmp_header_payload->icmp_type =
						ICMP6_ECHO_REQUEST;
				} else if (icmp_header_payload->icmp_type ==
					   ICMP_ECHOREPLY) {
					icmp_header_payload->icmp_type =
						ICMP6_ECHO_REPLY;
				} else {
					RTE_LOG(ERR,
						NAT64,
						"Unknown icmp type %d in icmp "
						"payload\n",
						icmp_header_payload->icmp_type);
					return -1;
				}

				// Recalculate ICMP checksum for IPv6 embeded
				icmp_header_payload->icmp_cksum = 0;
				uint32_t sum = rte_ipv6_phdr_cksum(
					new_ipv6_payload_header, 0
				);
				sum = __rte_raw_cksum(
					icmp_header_payload,
					rte_be_to_cpu_16(new_ipv6_payload_header
								 ->payload_len),
					sum
				);

				icmp_header_payload->icmp_cksum =
					~__rte_raw_cksum_reduce(sum);
				break;

			case IPPROTO_UDP: {
				struct rte_udp_hdr *udp_header =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_udp_hdr *,
						payload_offset
					);

				if (!udp_header) {
					RTE_LOG(ERR,
						NAT64,
						"Failed to get UDP header from "
						"mbuf\n");
					return -1;
				}

				// Recalculate UDP checksum for IPv6
				udp_header->dgram_cksum = 0;
				udp_header->dgram_cksum =
					rte_ipv6_udptcp_cksum_mbuf(
						mbuf,
						new_ipv6_payload_header,
						payload_offset
					);
				break;
			}
			case IPPROTO_TCP: {
				struct rte_tcp_hdr *tcp_header =
					rte_pktmbuf_mtod_offset(
						mbuf,
						struct rte_tcp_hdr *,
						payload_offset
					);

				if (!tcp_header) {
					RTE_LOG(ERR,
						NAT64,
						"Failed to get TCP header from "
						"mbuf\n");
					return -1;
				}

				// Recalculate TCP checksum for IPv6
				tcp_header->cksum = 0;
				tcp_header->cksum = rte_ipv6_udptcp_cksum_mbuf(
					mbuf,
					new_ipv6_payload_header,
					payload_offset
				);
				break;
			}

			default:
				RTE_LOG(ERR,
					NAT64,
					"Unknown protocol %d in icmp payload\n",
					new_ipv6_payload_header->proto);
				break;
			}
		}
	}

	icmp_header->icmp_cksum = 0;
	uint32_t sum = rte_ipv6_phdr_cksum(new_ipv6_header, 0);
	sum = __rte_raw_cksum(
		icmp_header, rte_be_to_cpu_16(new_ipv6_header->payload_len), sum
	);

	icmp_header->icmp_cksum = ~__rte_raw_cksum_reduce(sum);

	return 0;
}

/**
 * @brief Handles IPv4 packets for NAT64 translation.
 *
 * This function processes IPv4 packets according to RFC7915, handling IPv4
 * options, fragmentation, and protocol-specific translations (ICMP, TCP, UDP).
 *
 * @param nat64_config Pointer to NAT64 module configuration
 * @param packet Pointer to the packet structure containing the IPv4 header
 * @return 0 on success, -1 on failure
 */
static int
nat64_handle_v4(
	struct nat64_module_config *nat64_config, struct packet *packet
) {

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (!mbuf) {
		RTE_LOG(ERR, NAT64, "Failed to get mbuf from packet\n");
		return -1;
	}

	struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	if (!ipv4_header) {
		RTE_LOG(ERR, NAT64, "Failed to get IPv4 header from mbuf\n");
		return -1;
	}

	LOG_DBG(NAT64, "Processing IPv4 packet\n");

	uint32_t addr4 = ipv4_header->dst_addr;
	struct ip4to6 *entry = find_ip4to6(nat64_config, &addr4);
	if (!entry) {
		RTE_LOG(ERR,
			NAT64,
			"Failed to find IPv6 mapping for IPv4 "
			"address " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(addr4));
		return -1; //
	}

	LOG_DBG(NAT64,
		"Found IPv6 mapping for IPv4 address " IPv4_BYTES_FMT
		": " IPv6_BYTES_FMT "\n",
		IPv4_BYTES_LE(addr4),
		IPv6_BYTES(entry->ip6));

	// Check for IPv4 options and handle them according to RFC7915
	uint8_t ihl = (ipv4_header->version_ihl & RTE_IPV4_HDR_IHL_MASK);
	if (ihl > RTE_IPV4_MIN_IHL) {
		uint8_t *options = (uint8_t *)(ipv4_header + 1);
		uint8_t options_len =
			(ihl - RTE_IPV4_MIN_IHL) * RTE_IPV4_IHL_MULTIPLIER;
		uint8_t *options_end = options + options_len;

		LOG_DBG(NAT64,
			"Processing IPv4 options: IHL=%u, options_len=%u\n",
			ihl,
			options_len);

		// Parse options
		while (options < options_end) {
			uint8_t option_type = *options;

			// End of options list
			if (option_type == RTE_IPV4_HDR_OPT_EOL) {
				LOG_DBG(NAT64, "End of IPv4 options list\n");
				break;
			}

			// Check for source route options (LSRR=0x83, SSRR=0x89)
			if ((option_type == IPOPT_LSRR) ||
			    (option_type == IPOPT_SSRR)) {
				LOG_DBG(NAT64,
					"Source route option found (type "
					"0x%x), sending ICMP error\n",
					option_type);

				// RFC7915: Send ICMP error for source route
				// options
				struct rte_icmp_hdr *icmp_hdr =
					(struct rte_icmp_hdr *)(options_end + 1
					);

				icmp_hdr->icmp_type = ICMP_DEST_UNREACH;
				icmp_hdr->icmp_code = ICMP_SR_FAILED;
				icmp_hdr->icmp_cksum = 0;
				icmp_hdr->icmp_cksum = ~rte_raw_cksum(
					icmp_hdr, sizeof(struct rte_icmp_hdr)
				);
				if (icmp_hdr->icmp_cksum == 0) {
					icmp_hdr->icmp_cksum = 0xffff;
				}

				// FIXME: send icmp error packet instead drop
				LOG_DBG(NAT64,
					"Dropping packet with source route "
					"option\n");
				return -1;
			}

			// Skip to next option
			if (option_type == RTE_IPV4_HDR_OPT_NOP) {
				options++;
				LOG_DBG(NAT64, "Skipping NOP option\n");
			} else {
				if (options + 1 >= options_end) {
					LOG_DBG(NAT64,
						"Malformed IPv4 options: "
						"option extends beyond options "
						"end\n");
					return -1;
				}
				uint8_t option_len = options[1];
				if (option_len < 2 ||
				    options + option_len > options_end) {
					LOG_DBG(NAT64,
						"Invalid IPv4 option length: "
						"%u\n",
						option_len);
					return -1;
				}
				LOG_DBG(NAT64,
					"Skipping option type 0x%x, length "
					"%u\n",
					option_type,
					option_len);
				options += option_len;
			}
		}
	}

	// ipv4 header may be min 20 max 60 bytes
	int16_t delta =
		sizeof(struct rte_ipv6_hdr) - (packet->transport_header.offset -
					       packet->network_header.offset);

	// Extract fragment information
	uint16_t frag_data = rte_be_to_cpu_16(ipv4_header->fragment_offset);
	uint16_t frag_offset = (frag_data & RTE_IPV4_HDR_OFFSET_MASK) << 3;
	bool more_fragments = !!(frag_data & RTE_IPV4_HDR_MF_FLAG);
	uint8_t is_fragmented =
		frag_data & (RTE_IPV4_HDR_MF_FLAG | RTE_IPV4_HDR_OFFSET_MASK);

	if (is_fragmented) {
		// Calculate fragment size
		uint16_t total_len =
			rte_be_to_cpu_16(ipv4_header->total_length);
		uint16_t header_len =
			(ipv4_header->version_ihl & RTE_IPV4_HDR_IHL_MASK) * 4;
		uint16_t frag_size = total_len - header_len;

		// Validate fragment parameters
		if (validate_fragment_params(
			    frag_offset,
			    frag_size,
			    total_len,
			    more_fragments,
			    ipv4_header->next_proto_id == IPPROTO_ICMP
		    ) != 0) {
			return -1;
		}
		// Add space for fragment extension header
		delta += RTE_IPV6_FRAG_HDR_SIZE;
	}

	char *nmbuf = NULL;
	if (delta >= 0) {
		nmbuf = rte_pktmbuf_prepend(mbuf, delta);
	} else {
		// RFC7915 1.2 ( -> .. -> RFC2765 1.1) does not translate any
		// IPv4 options.
		LOG_DBG(NAT64,
			"ip4 header bigger than ip6 header(s) " IPv4_BYTES_FMT
			" -> " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(ipv4_header->src_addr),
			IPv4_BYTES_LE(addr4));
		RTE_LOG(ERR,
			NAT64,
			"no support translation with ip4 header bigger than "
			"ip6 header(s)\n");
		return -1;
	}
	if (!nmbuf) {
		RTE_LOG(ERR, NAT64, "Failed to resize mbuf\n");
		return -1;
	}

	rte_memcpy(
		rte_pktmbuf_mtod(mbuf, char *),
		rte_pktmbuf_mtod_offset(mbuf, char *, delta),
		packet->network_header.offset
	);

	struct rte_ipv6_hdr *new_ipv6_header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	if (!new_ipv6_header) {
		RTE_LOG(ERR, NAT64, "Failed to get new IPv6 header from mbuf\n"
		);
		return -1;
	}

	if (copy_ipv4_to_ipv6_hdr(
		    mbuf,
		    ipv4_header,
		    new_ipv6_header,
		    packet->network_header.offset,
		    ADDR_OF(&nat64_config->prefixes.prefixes
		    )[entry->prefix_index]
			    .prefix,
		    entry->ip6,
		    is_fragmented,
		    delta,
		    false
	    )) {
		RTE_LOG(ERR,
			NAT64,
			"Failed to copy IPv4 header to IPv6 header\n");
		return -1;
	}

	packet->transport_header.offset += delta;
	if (new_ipv6_header->proto == IPPROTO_ICMP) {
		new_ipv6_header->proto = IPPROTO_ICMPV6;
		int result = icmp_v4_to_v6(
			nat64_config,
			packet,
			new_ipv6_header,
			ADDR_OF(&nat64_config->prefixes.prefixes
			)[entry->prefix_index]
				.prefix,
			entry->ip6
		);
		if (result) {
			LOG_DBG(NAT64,
				"ICMP translation failed, dropping packet\n");
			return -1;
		}
	} else if (new_ipv6_header->proto == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);

		if (!udp_header) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get UDP header from mbuf\n");
			return -1;
		}

		// Recalculate UDP checksum for IPv6
		udp_header->dgram_cksum = 0;
		// Use rte_ipv6_udptcp_cksum_mbuf for more accurate checksum
		// calculation
		udp_header->dgram_cksum = rte_ipv6_udptcp_cksum_mbuf(
			mbuf, new_ipv6_header, packet->transport_header.offset
		);
		LOG_DBG(NAT64,
			"UDP ipv6 phd checksum: %x\n",
			rte_ipv6_phdr_cksum(new_ipv6_header, 0));
		LOG_DBG(NAT64,
			"UDP checksum calculated: 0x%04X\n",
			rte_be_to_cpu_16(udp_header->dgram_cksum));
	} else if (new_ipv6_header->proto == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);

		if (!tcp_header) {
			RTE_LOG(ERR,
				NAT64,
				"Failed to get TCP header from mbuf\n");
			return -1;
		}

		// Recalculate TCP checksum for IPv6
		tcp_header->cksum = 0;
		tcp_header->cksum = rte_ipv6_udptcp_cksum_mbuf(
			mbuf, new_ipv6_header, packet->transport_header.offset
		);
	}

	struct rte_ether_hdr *eth_header =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	if (!eth_header) {
		RTE_LOG(ERR, NAT64, "Failed to get Ethernet header from mbuf\n"
		);
		return -1;
	}
	eth_header->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	return 0;
}

/**
 * @brief Main packet processing function for NAT64 translation
 *
 * This function is the main entry point for NAT64 packet processing. It handles
 * both IPv4-to-IPv6 and IPv6-to-IPv4 translations according to RFC7915.
 * For each packet in the input list, it:
 * 1. Determines packet IP version
 * 2. Routes to appropriate handler (nat64_handle_v4 or nat64_handle_v6)
 * 3. Either outputs translated packet or drops on failure
 *
 * The function implements stateless NAT64 translation, meaning:
 * - No connection tracking
 * - No dynamic address mapping
 * - Fixed prefix and address mapping configuration
 *
 * @param dp_config Pointer to dataplane configuration (unused but required by
 * API)
 * @param module_data Pointer to NAT64 module data containing:
 *                    - Address mappings
 *                    - NAT64 prefixes
 *                    - MTU settings
 * @param packet_front Pointer to packet front structure containing:
 *                     - Input packet list to process
 *                     - Output list for translated packets
 *                     - Drop list for failed translations
 *
 * @note Packets are processed one at a time to maintain ordering
 * @note Translation failures result in packet being moved to drop list
 * @note The function assumes packets have valid Ethernet and IP headers
 *
 * @see nat64_handle_v4() For IPv4-to-IPv6 translation
 * @see nat64_handle_v6() For IPv6-to-IPv4 translation
 * @see RFC7915 - IP/ICMP Translation Algorithm
 */
void
nat64_handle_packets(
	struct dp_config *dp_config,
	struct module_data *module_data,
	struct packet_front *packet_front
) {
	(void)dp_config; // Unused parameter

	struct nat64_module_config *nat64_config = container_of(
		module_data, struct nat64_module_config, module_data
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int result = -1; // Default to dropping unknown ether types

		// TODO: RTE_ETH_IS_IPV4_HDR ?
		if (packet->network_header.type ==
		    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
			LOG_DBG(NAT64, "Start processing IPv4 packet\n");
			result = nat64_handle_v4(nat64_config, packet);
		} else if (packet->network_header.type ==
			   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
			LOG_DBG(NAT64, "Start processing IPv6 packet\n");
			result = nat64_handle_v6(nat64_config, packet);
		}

		if (result) {
			LOG_DBG(NAT64,
				"Dropping packet due to translation failure\n");
			packet_front_drop(packet_front, packet);
		} else {
			LOG_DBG(NAT64, "Successfully translated packet\n");
			packet_front_output(packet_front, packet);
		}
	}
}

struct module *
new_module_nat64() {

#ifdef DEBUG_NAT64
	rte_log_set_level(RTE_LOGTYPE_NAT64, RTE_LOG_DEBUG);
#endif
	struct nat64_module *module =
		(struct nat64_module *)malloc(sizeof(*module));

	if (module == NULL) {
		RTE_LOG(ERR, NAT64, "Failed to allocate memory for module\n");
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "nat64"
	);
	module->module.handler = nat64_handle_packets;

	return &module->module;
}
