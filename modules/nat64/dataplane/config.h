#pragma once

#include "common/lpm.h"
#include "common/network.h"

#include "dataplane/config/zone.h"

/**
 * @brief Structure representing a mapping from IPv4 to IPv6
 *
 * This structure is used to map an IPv4 address to an IPv6 address.
 * It includes IPv4 and IPv6 addresses and an index to the used IPv6 prefix.
 */
struct ip4to6 {
	/**
	 * @brief IPv4 address in network byte order
	 */
	uint32_t ip4;

	/**
	 * @brief IPv6 address (16 bytes)
	 */
	uint8_t ip6[16];

	/**
	 * @brief Index of the used IPv6 prefix
	 *
	 * This field stores the index of the used IPv6 prefix in the
	 * nat64_module_config.prefixes array.
	 */
	size_t prefix_index;
};

/**
 * @brief Structure representing an IPv6 prefix for NAT64
 *
 * This structure holds a 12-byte IPv6 prefix used in NAT64 translations.
 */
struct nat64_prefix {
	/**
	 * @brief 12-byte IPv6 prefix
	 *
	 * This field contains the 12-byte prefix used to identify IPv6
	 * addresses in NAT64 translations.
	 */
	uint8_t prefix[12];
};

/**
 * @brief Configuration structure for the NAT64 module
 *
 * This structure holds the configuration settings for the NAT64 module,
 * including LPM tables and arrays for address mappings.
 */
struct nat64_module_config {
	struct module_data module_data;
	/* Address mapping configuration */
	struct {
		uint64_t count;	     /**< Number of mappings */
		struct ip4to6 *list; /**< List of IPv4 to IPv6 mappings */
		struct lpm v4_to_v6; /**< IPv4 to IPv6 LPM table */
		struct lpm v6_to_v4; /**< IPv6 to IPv4 LPM table */
	} mappings;

	/* NAT64 prefix configuration */
	struct {
		struct nat64_prefix *prefixes; /**< Array of IPv6 prefixes */
		uint64_t count;		       /**< Number of prefixes */
	} prefixes;

	/* MTU configuration */
	struct {
		uint16_t ipv4; /**< IPv4 MTU limit */
		uint16_t ipv6; /**< IPv6 MTU limit */
	} mtu;
};
