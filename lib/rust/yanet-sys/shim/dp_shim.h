// Dataplane helpers over the C the Rust handler runtime builds on.
//
// The shim holds the packet redirect of a module to a device entry, the
// headroom growth of a frame and the packet encapsulation routines, whose
// sources the build compiles in. It is compiled into each module plugin with
// hidden visibility, so the routing logic (recirculation budget, device entry
// scheduling, pending counters) and the encapsulation stay the C code every C
// module runs, and a plugin exports and imports none of it: the symbols are
// hidden, so a call resolves inside the plugin at load time. A Rust call site
// still reaches the function through a plugin-local global offset table slot,
// one extra load per call, because stable rustc emits a non-relaxable
// GOT-relative reference.
#pragma once

#include <stdint.h>

struct module_ectx;
struct module_device_target;
struct packet;
struct packet_front;
struct rte_mbuf;

// bindgen is told to read the declarations without the attribute.
#ifdef YANET_SYS_BINDGEN
#define YANET_SYS_DP_HIDDEN
#else
#define YANET_SYS_DP_HIDDEN __attribute__((visibility("hidden")))
#endif

// Routes a packet to a module device target, to its input entry when the
// selector is non-zero and to its output entry otherwise.
//
// The packet lands on the schedule of the target entry and counts as pending
// input or output on the packet front. An exhausted recirculation budget
// drops the packet on the front instead and counts the drop on the entry.
YANET_SYS_DP_HIDDEN void
yanet_sys_dp_redirect(
	struct module_ectx *module_ectx,
	struct packet_front *packet_front,
	struct module_device_target *target,
	int to_input,
	struct packet *packet
);

// packet_headroom_prepend (lib/dataplane/packet/data.h): grows the first
// segment of the mbuf at the front; NULL, mbuf unchanged, when out of room.
//
// The growth is by len bytes into the headroom the packet descriptor sharing
// the buffer leaves; the return value is the new start of the data. The Rust
// runtime inlines its own twin; a test compares the two.
YANET_SYS_DP_HIDDEN char *
yanet_sys_dp_headroom_prepend(struct rte_mbuf *mbuf, uint16_t len);

// packet_front_pass (lib/dataplane/module/packet_front.h): moves the whole
// input list to the output with the input counters.
//
// The Rust runtime inlines its own twin; a test compares the two.
YANET_SYS_DP_HIDDEN void
yanet_sys_dp_front_pass(struct packet_front *packet_front);

// The packet encapsulation of lib/dataplane/packet, as the C modules call it.
//
// Each returns 0 on success and -1 when the packet cannot take the header.

// packet_ip4_encap: prepends an outer IPv4 header of dst and src to an IPv4
// or IPv6 packet.
//
// The addresses are 4 bytes each in network order; dscp_flag is a
// DSCP_MARK_* value.
YANET_SYS_DP_HIDDEN int
yanet_sys_dp_ip4_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
);

// packet_ip6_encap: as yanet_sys_dp_ip4_encap with an outer IPv6 header
// (16-byte addresses).
YANET_SYS_DP_HIDDEN int
yanet_sys_dp_ip6_encap(
	struct packet *packet,
	const uint8_t *dst,
	const uint8_t *src,
	uint8_t dscp_flag
);

// packet_mpls_encap: prepends one MPLS label stack entry to the packet's
// network header.
YANET_SYS_DP_HIDDEN int
yanet_sys_dp_mpls_encap(
	struct packet *packet,
	uint32_t label,
	uint8_t tc,
	uint8_t bottom,
	uint8_t ttl
);

// packet_ip4_encap_udp: wraps the network packet in UDP over IPv4 (4-byte
// addresses, network order) with the given host-order ports.
YANET_SYS_DP_HIDDEN int
yanet_sys_dp_ip4_encap_udp(
	struct packet *packet,
	const uint8_t *src,
	const uint8_t *dst,
	uint16_t src_port,
	uint16_t dst_port
);

// packet_ip6_encap_udp: as yanet_sys_dp_ip4_encap_udp over IPv6 (16-byte
// addresses).
YANET_SYS_DP_HIDDEN int
yanet_sys_dp_ip6_encap_udp(
	struct packet *packet,
	const uint8_t *src,
	const uint8_t *dst,
	uint16_t src_port,
	uint16_t dst_port
);

// packet_decap: removes the IP-in-IP or GRE tunnel headers of the packet.
YANET_SYS_DP_HIDDEN int
yanet_sys_dp_decap(struct packet *packet);
