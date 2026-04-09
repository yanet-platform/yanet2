#pragma once

struct packet;

/*
 * Clamp TCP MSS option for IPv6 packets to avoid fragmentation
 * after tunnel encapsulation.
 *
 * Only processes TCP SYN packets (without RST). For such packets:
 * - If an MSS option exists and exceeds FIX_MSS_SIZE (1220),
 *   it is clamped down and the TCP checksum is updated incrementally.
 * - If no MSS option exists and there is room in the TCP header,
 *   a new MSS option with DEFAULT_MSS_SIZE (536) is inserted.
 *
 * Does nothing for non-TCP or non-SYN packets.
 */
void
fix_mss_ipv6(struct packet *packet);
