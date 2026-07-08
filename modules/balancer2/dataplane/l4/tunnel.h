#pragma once

struct packet_context;

/*
 * Tunnel a packet whose inner layer is IPv4.
 *
 * Encapsulates the packet in an IP-in-IP tunnel towards the resolved
 * real server, embeds the client source IP into the outer header,
 * and optionally wraps in GRE.
 *
 * MSS clamping is not performed — it only applies to inner IPv6.
 */
int
tunnel_ip4_packet(struct packet_context *pkt_ctx);

/*
 * Tunnel a packet whose inner layer is IPv6.
 *
 * Encapsulates the packet in an IP-in-IP tunnel towards the resolved
 * real server, embeds the client source IP into the outer header,
 * and optionally wraps in GRE.
 *
 * MSS clamping is applied when the virtual service is configured
 * to enforce a maximum segment size.
 */
int
tunnel_ip6_packet(struct packet_context *pkt_ctx);
