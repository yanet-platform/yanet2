#include <rte_ip.h>

#include "checksum.h"
#include "dscp.h"

int
dscp_mark_v4(struct rte_ipv4_hdr *ip4_hdr, struct dscp_config config) {
	uint8_t mark = ip4_hdr->type_of_service & DSCP_MARK_MASK;
	if (config.flag & DSCP_MARK_DEFAULT && mark != 0) {
		// do not remark
		return -1;
	}

	uint16_t checksum = ~rte_be_to_cpu_16(ip4_hdr->hdr_checksum);
	checksum = csum_minus(checksum, mark);
	uint8_t new_mark = config.mark << DSCP_MARK_SHIFT;
	checksum = csum_plus(checksum, new_mark);
	ip4_hdr->hdr_checksum = ~rte_cpu_to_be_16(checksum);

	uint8_t ecn = ip4_hdr->type_of_service & DSCP_ECN_MASK;
	ip4_hdr->type_of_service = new_mark | ecn;
	return 0;
}

static inline uint8_t
get_ipv6_tc(rte_be32_t vtc_flow) {
	uint32_t v = rte_be_to_cpu_32(vtc_flow);
	return v >> RTE_IPV6_HDR_TC_SHIFT;
}

static inline rte_be32_t
set_ipv6_tc(rte_be32_t vtc_flow, uint32_t tc) {
	// Shift by the length of the Flow Label - 20-bit.
	uint32_t v = rte_cpu_to_be_32(tc << RTE_IPV6_HDR_TC_SHIFT);
	vtc_flow &= ~rte_cpu_to_be_32(RTE_IPV6_HDR_TC_MASK);
	return (v | vtc_flow);
}

int
dscp_mark_v6(struct rte_ipv6_hdr *ip6_hdr, struct dscp_config config) {
	uint8_t tc = get_ipv6_tc(ip6_hdr->vtc_flow);
	uint8_t mark = tc & DSCP_MARK_MASK;
	if (config.flag & DSCP_MARK_DEFAULT && mark != 0) {
		// do not remark
		return -1;
	}
	uint8_t new_mark = config.mark << DSCP_MARK_SHIFT;
	ip6_hdr->vtc_flow = set_ipv6_tc(ip6_hdr->vtc_flow, new_mark);
	return 0;
}
