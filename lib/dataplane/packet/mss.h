#pragma once

#include <stdint.h>

struct packet;

enum packet_set_mss_result {
	/* MSS was clamped, inserted, or the packet did not need fixing. */
	packet_set_mss_ok = 0,
	/*
	 * TCP header or options are malformed, or the header already
	 * occupies the full 60 bytes so a new option cannot fit in the
	 * data_off field. The packet is left untouched.
	 */
	packet_set_mss_malformed,
	/* mbuf has no headroom at the front to prepend the new option. */
	packet_set_mss_no_headroom,
};

/*
 * Normalize the TCP MSS option on an IPv6 TCP SYN packet in a single pass.
 *
 *   - If the packet is not an IPv6 TCP SYN (or its RST flag is set), it is
 *     left untouched.
 *   - If an MSS option is present and its value exceeds `clamp_mss`, the
 *     option is rewritten to `clamp_mss`. Lower values are kept as-is.
 *   - If no MSS option is present, a new option with value `insert_mss`
 *     is inserted immediately after the fixed TCP header.
 *
 * TCP checksum and IPv6 payload length are updated as needed.
 */
enum packet_set_mss_result
packet_set_mss(struct packet *packet, uint16_t clamp_mss, uint16_t insert_mss);
