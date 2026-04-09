#pragma once

struct l4_packet_context;

/*
 * Tunnel a packet whose inner layer is IPv4.
 *
 * Encapsulates the packet in an IP-in-IP tunnel towards the resolved
 * real server, embeds the client source IP into the outer header,
 * optionally wraps in GRE, and updates forwarding statistics.
 *
 * MSS clamping is not performed — it only applies to inner IPv6.
 */
void
tunnel_ipv4_packet(struct l4_packet_context *pkt_ctx);

/*
 * Tunnel a packet whose inner layer is IPv6.
 *
 * Encapsulates the packet in an IP-in-IP tunnel towards the resolved
 * real server, embeds the client source IP into the outer header,
 * optionally wraps in GRE, and updates forwarding statistics.
 *
 * MSS clamping is applied when the VS has balancer_vs_fix_mss set.
 */
void
tunnel_ipv6_packet(struct l4_packet_context *pkt_ctx);
