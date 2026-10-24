// Dataplane helpers over the C the Rust handler runtime builds on.
//
// The shim holds the packet redirect of a module to a device entry. It is
// compiled into each module plugin with hidden visibility, so the routing
// logic (recirculation budget, device entry scheduling, pending counters)
// stays the C code every C module runs, and a plugin exports and imports none
// of it: the symbols are hidden, so a call resolves inside the plugin at load
// time. A Rust call site still reaches the function through a plugin-local
// global offset table slot, one extra load per call, because stable rustc
// emits a non-relaxable GOT-relative reference.
#pragma once

struct module_ectx;
struct module_device_target;
struct packet;
struct packet_front;

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
