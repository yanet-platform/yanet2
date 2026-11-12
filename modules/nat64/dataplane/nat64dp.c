/**
 * @file nat64dp.c
 * @brief Stateless NAT64 module dataplane implementation.
 *
 * The module owns the address-resolution policy: static IPv6<->IPv4 mapping
 * and prefix tables configured through the control plane. The RFC7915
 * packet translation itself lives in the shared
 * lib/dataplane/packet/nat64 library.
 *
 * References:
 * - RFC7915: IP/ICMP Translation Algorithm
 */

/* System headers */
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

/* DPDK headers */
#include <rte_common.h>
#include <rte_config.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_log.h>
#include <rte_mbuf.h>

#include "yanet_build_config.h"

/* Project headers */
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/nat64/nat64.h"
#include "lib/dataplane/packet/nat64/nat64_log.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "modules/nat64/dataplane/nat64dp.h"

/**
 * @brief Finds a mapping from IPv6 to IPv4 address
 *
 * This function searches the LPM (Longest Prefix Match) table for a mapping
 * from an IPv6 address to an IPv4 address. The search is performed using the
 * IPv6 address as the lookup key.
 *
 * @param config Pointer to the NAT64 module configuration containing mapping
 * tables. Must not be NULL.
 * @param ip6 Pointer to the IPv6 address to look up (16 bytes). Must not be
 * NULL.
 *
 * @return Pointer to the found mapping structure on success, NULL on failure
 *
 * @note The function assumes the IPv6 address is in network byte order
 */
static struct ip4to6 *
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
 */
static struct ip4to6 *
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
 * @brief Builds the translation context from the module configuration
 */
static struct nat64_ctx
nat64_ctx_from_config(struct nat64_module_config *config) {
	return (struct nat64_ctx){
		.mtu_ipv4 = config->mtu.ipv4,
		.mtu_ipv6 = config->mtu.ipv6,
		.malformed = &config->stats.malformed_packets,
	};
}

/**
 * @brief Handles IPv6 packets for NAT64 translation
 *
 * Resolves the IPv4 source of the translation from the static mapping
 * table and applies the configured unknown-mapping policy before handing
 * the packet to the shared translation core.
 *
 * @param nat64_config Pointer to the NAT64 module configuration
 * @param packet Pointer to the packet structure containing the IPv6 header
 * @return 0 on success, -1 on failure
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
			"not found mapping for " IPv6_BYTES_FMT "\n",
			IPv6_BYTES(ipv6_header->src_addr));
		if (nat64_config->prefixes.drop_unknown_prefix) {
			uint32_t prefix_index = lpm_lookup(
				&nat64_config->prefixes.v6_prefixes,
				12,
				(uint8_t *)&ipv6_header->src_addr
			);
			if (prefix_index == LPM_VALUE_INVALID) {
				LOG_DBG(NAT64,
					"Dropping packet due to unknown IPv6 "
					"prefix\n");
				return -1;
			}
		}
		if (nat64_config->mappings.drop_unknown_mapping) {
			LOG_DBG(NAT64,
				"Dropping packet due to no IPv6->IPv4 mapping\n"
			);
			return -1;
		}
		return 0;
	}

	LOG_DBG(NAT64,
		"found mapping " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT "\n",
		IPv6_BYTES(ipv6_header->src_addr),
		IPv4_BYTES(RTE_BE32(new_src_addr->ip4)));

	struct nat64_ctx ctx = nat64_ctx_from_config(nat64_config);
	return nat64_translate_v6_to_v4(&ctx, packet, new_src_addr->ip4);
}

/**
 * @brief Handles IPv4 packets for NAT64 translation
 *
 * Resolves the IPv6 source address and prefix of the translation from the
 * static mapping table and applies the configured unknown-mapping policy
 * before handing the packet to the shared translation core.
 *
 * @param nat64_config Pointer to the NAT64 module configuration
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
		LOG_DBG(NAT64,
			"Failed to find IPv6 mapping for IPv4 "
			"address " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(addr4));
		if (nat64_config->mappings.drop_unknown_mapping) {
			LOG_DBG(NAT64,
				"Dropping packet due to no IPv4->IPv6 mapping\n"
			);
			return -1;
		}
		return 0;
	}

	LOG_DBG(NAT64,
		"Found IPv6 mapping for IPv4 address " IPv4_BYTES_FMT
		": " IPv6_BYTES_FMT "\n",
		IPv4_BYTES_LE(addr4),
		IPv6_BYTES(entry->ip6));

	struct nat64_ctx ctx = nat64_ctx_from_config(nat64_config);
	return nat64_translate_v4_to_v6(
		&ctx,
		packet,
		ADDR_OF(&nat64_config->prefixes.prefixes)[entry->prefix_index]
			.prefix,
		entry->ip6
	);
}

/**
 * @brief Main packet processing function for NAT64 translation
 *
 * Routes each input packet to the IPv4 or IPv6 handler by EtherType and
 * moves it to the output list on success or the drop list on failure.
 *
 * @param dp_worker Pointer to the dataplane worker (unused)
 * @param module_ectx Pointer to the module execution context carrying the
 * module configuration
 * @param packet_front Pointer to packet front structure containing the
 * input, output and drop lists
 */
void
nat64_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	(void)dp_worker;

	struct nat64_module_config *nat64_config = container_of(
		module_ectx->abs_cp_module,
		struct nat64_module_config,
		cp_module
	);

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		int result = -1; // Default to dropping unknown ether types

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
			LOG_DBG(NAT64,
				"Packet successfully translated or passed "
				"through\n");
			packet_front_output(packet_front, packet);
		}
	}
}

static void
nat64_module_commit(struct dp_config *dp_config, struct cp_module *cp_module) {
	(void)dp_config;
	(void)cp_module;
}

struct module *
new_module_nat64() {
	struct nat64_module *module =
		(struct nat64_module *)malloc(sizeof(*module));

	if (module == NULL) {
		RTE_LOG(ERR, NAT64, "Failed to allocate memory for module\n");
		return NULL;
	}

	// The loader copies every field of the returned descriptor, so
	// heap garbage must not survive in the ones this constructor
	// leaves unset.
	memset(module, 0, sizeof(*module));

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "nat64"
	);
	module->module.handler = nat64_handle_packets;
	module->module.commit_handler = nat64_module_commit;

	return &module->module;
}
