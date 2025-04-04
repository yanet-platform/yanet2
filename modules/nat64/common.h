#pragma once

#ifndef IPv4_BYTES
/**
 * @brief Format string for printing IPv4 addresses
 *
 * This format string is used to print an IPv4 address in the standard
 * dotted-decimal format.
 */
#define IPv4_BYTES_FMT "%" PRIu8 ".%" PRIu8 ".%" PRIu8 ".%" PRIu8

/**
 * @brief Macro to extract IPv4 address bytes
 *
 * This macro extracts the four bytes from a 32-bit integer representing an IPv4
 * address. It can be used in conjunction with printf and IPv4_BYTES_FMT to
 * print the address.
 *
 * The byte order is considered as big-endian, meaning that the first extracted
 * byte is the most significant (MSB) and represents the first octet of the IP
 * address.
 *
 * @param addr 32-bit integer representing an IPv4 address.
 *
 * @example
 * ```c
 * uint32_t ipv4_addr = 0xC0A80101; // 192.168.1.1
 * printf(IPv4_BYTES_FMT "\n", IPv4_BYTES(ipv4_addr));
 * ```
 */
#define IPv4_BYTES(addr)                                                       \
	(uint8_t)(((addr) >> 24) & 0xFF), (uint8_t)(((addr) >> 16) & 0xFF),    \
		(uint8_t)(((addr) >> 8) & 0xFF), (uint8_t)((addr) & 0xFF)

/**
 * @brief Macro to extract IPv4 address bytes in reverse order for little-endian
 * systems
 *
 * This macro is designed to work with systems that use little-endian byte
 * order. It extracts the four bytes from a 32-bit integer representing an IPv4
 * address in reverse order. Use this if you need the least significant byte
 * (LSB) as the first byte.
 *
 * @param addr 32-bit integer representing an IPv4 address.
 */
#define IPv4_BYTES_LE(addr)                                                    \
	(uint8_t)((addr) & 0xFF), (uint8_t)(((addr) >> 8) & 0xFF),             \
		(uint8_t)(((addr) >> 16) & 0xFF),                              \
		(uint8_t)(((addr) >> 24) & 0xFF)
#endif

#ifndef IPv6_BYTES
/**
 * @brief Format string for printing IPv6 addresses
 *
 * This format string is used to print an IPv6 address in the standard
 * colon-separated hexadecimal format.
 */
#define IPv6_BYTES_FMT                                                         \
	"%02x%02x:%02x%02x:%02x%02x:%02x%02x:"                                 \
	"%02x%02x:%02x%02x:%02x%02x:%02x%02x"

/**
 * @brief Macro to extract IPv6 address bytes
 *
 * This macro extracts the 16 bytes from an array representing an IPv6 address.
 * It can be used in conjunction with printf and IPv6_BYTES_FMT to print the
 * address.
 *
 * @param addr Pointer to an array of 16 uint8_t values representing an IPv6
 * address.
 *
 * @example
 * ```c
 * uint8_t ipv6_addr[16] = {0x20, 0x01, 0x0d, 0xb8, 0x85, 0xa3, 0x00, 0x00,
 * 0x00, 0x00, 0x8a, 0x2e, 0x03, 0x70, 0x73, 0x34}; printf(IPv6_BYTES_FMT "\n",
 * IPv6_BYTES(ipv6_addr));
 * ```
 */
#define IPv6_BYTES(addr)                                                       \
	addr[0], addr[1], addr[2], addr[3], addr[4], addr[5], addr[6],         \
		addr[7], addr[8], addr[9], addr[10], addr[11], addr[12],       \
		addr[13], addr[14], addr[15]
#endif

/**
 * @brief Format string for printing IPv6 addresses using 16-bit words
 *
 * This format string is used to print an IPv6 address in the standard
 * colon-separated hexadecimal format.
 */
#define IPv6_BYTES_FMT_U32                                                     \
	"%04x:%04x:%04x:%04x:"                                                 \
	"%04x:%04x:%04x:%04x"

/**
 * @brief Macro to extract IPv6 address words
 *
 * This macro extracts the 8 16-bit words from an array representing an IPv6
 * address. It can be used in conjunction with printf and IPv6_BYTES_FMT_U32 to
 * print the address.
 *
 * @param addr Pointer to an array of 4 uint32_t values representing the IPv6
 * address.
 *
 * @example
 * ```c
 * uint32_t ipv6_addr[4] = {0x20010db8, 0x85a30000, 0x00008a2e, 0x03707334};
 * printf(IPv6_BYTES_FMT_U32 "\n", IPv6_BYTES_U32(ipv6_addr));
 * ```
 */
#define IPv6_BYTES_U32(addr)                                                   \
	(addr[0] >> 16), (addr[0] & 0xFFFF), (addr[1] >> 16),                  \
		(addr[1] & 0xFFFF), (addr[2] >> 16), (addr[2] & 0xFFFF),       \
		(addr[3] >> 16), (addr[3] & 0xFFFF)

/**
 * @brief Macro to set an IPv4-mapped IPv6 address
 *
 * This macro sets an IPv6 address to be a mapping of an IPv4 address.
 * It copies a 12-byte prefix into the first 12 bytes of the IPv6 address
 * and the 4-byte IPv4 address into the last 4 bytes.
 *
 * @param ip6 Pointer to the array of 16 uint8_t values representing the IPv6
 * address.
 * @param prefix Pointer to the 12-byte array representing the IPv6 prefix.
 * @param ip4 Pointer to the 4-byte array representing the IPv4 address.
 *
 * @example
 * ```c
 * const uint8_t ipv6_prefix[12] = { 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF };
 * // IPv4-mapped IPv6 prefix uint8_t ipv4_addr[4] = { 192, 168, 1, 1 }; //
 * 192.168.1.1 uint8_t ipv6_addr[16]; SET_IPV4_MAPPED_IPV6(ipv6_addr,
 * ipv6_prefix, ipv4_addr); printf(IPv6_BYTES_FMT "\n", IPv6_BYTES(ipv6_addr));
 * // Output: ::ffff:192.168.1.1
 * ```
 */
#define SET_IPV4_MAPPED_IPV6(ip6, prefix, ip4)                                 \
	do {                                                                   \
		rte_memcpy(ip6, prefix, 12);                                   \
		rte_memcpy(ip6[12], ip4, 4);                                   \
	} while (0)

#ifdef DEBUG_NAT64

/**
 * @brief Debug logging macro with optional code fragment
 *
 * This macro logs a debug message using the RTE_LOG function.
 * It also allows for an optional code fragment `f` to be executed before the
 * logging happens, which can be used to prepare data for logging.
 *
 * @param app The log type identifier. This is typically a predefined constant
 * used to categorize log messages.
 * @param f An optional code fragment that will be executed before the logging
 * happens. This can be used to prepare data for logging.
 * @param fmt The format string for the log message.
 * @param ... Additional arguments matching the format string.
 *
 * @example
 * ```c
 * int value = 42;
 * LOG_DBGX(NAT64, , "Debug value: %d\n", value);
 * LOG_DBGX(NAT64, printf("Before logging: value = %d\n", value);, "Debug value:
 * %d\n", value);
 * ```
 */
#define LOG_DBGX(app, f, fmt, ...)                                             \
	do {                                                                   \
		f RTE_LOG(DEBUG, app, fmt, ##__VA_ARGS__);                     \
	} while (0)

/**
 * @brief Debug logging macro
 *
 * This macro logs a debug message using the RTE_LOG function.
 *
 * @param app The log type identifier. This is typically a predefined constant
 * used to categorize log messages.
 * @param fmt The format string for the log message.
 * @param ... Additional arguments matching the format string.
 *
 * @example
 * ```c
 * int value = 42;
 * LOG_DBG(NAT64, "Debug value: %d\n", value);
 * ```
 */
#define LOG_DBG(app, fmt, ...)                                                 \
	do {                                                                   \
		RTE_LOG(DEBUG, app, fmt, ##__VA_ARGS__);                       \
	} while (0)

#else

/**
 * @brief No-operation (noop) debug logging macro
 *
 * This macro is a no-operation placeholder for debug logging.
 * It does nothing and is used in-place of an actual logging macro.
 *
 * All parameters are ignored.
 *
 * @param app The log type identifier. This parameter is ignored.
 * @param f An optional code fragment that will be executed before the logging
 * happens. This parameter is ignored.
 * @param fmt The format string for the log message. This parameter is ignored.
 * @param ... Additional arguments matching the format string. These parameters
 * are ignored.
 */
#define LOG_DBGX(app, f, fmt, ...) (void)(0)

/**
 * @brief No-operation (noop) debug logging macro without optional code fragment
 *
 * This macro is a no-operation placeholder for debug logging.
 * It does nothing and is used in-place of an actual logging macro.
 *
 * All parameters are ignored.
 *
 * @param app The log type identifier. This parameter is ignored.
 * @param fmt The format string for the log message. This parameter is ignored.
 * @param ... Additional arguments matching the format string. These parameters
 * are ignored.
 */
#define LOG_DBG(app, fmt, ...) (void)(0)

#endif
