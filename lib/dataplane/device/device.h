#pragma once

#define DEVICE_TYPE_LEN 80

struct packet_front;
struct device_ectx;
struct dp_worker;
struct dp_config;
struct cp_device;

typedef void (*device_handler)(
	struct dp_worker *dp_worker,
	struct device_ectx *device_ectx,
	struct packet_front *packet_front
);

// Commit handler: a preparation hook, run exactly once per config
// item, before any worker can see it.
//
// Runs inside the dataplane address space, from exactly one thread,
// when the item first appears in a published generation; a later
// generation carrying the same item does not run it again. Only
// generation-invariant derivation — from the item's own content
// alone, like absolutizing its own internal pointers — may be
// written. Controlplane-owned relative fields stay authoritative,
// and anything per-worker or referencing generation-owned memory
// belongs in the ectx layer instead. The thread holds no
// control-plane lock in production and is the installer holding the
// control-plane configuration lock in the harness; taking that lock
// self-deadlocks.
typedef void (*device_commit_handler)(
	struct dp_config *dp_config, struct cp_device *cp_device
);

struct device {
	char name[DEVICE_TYPE_LEN];
	device_handler input_handler;
	device_handler output_handler;
	device_commit_handler commit_handler;
};

typedef struct device *(*device_load_handler)();
