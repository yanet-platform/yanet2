// The C surface the Rust dataplane binds to; bindgen reads only this file.
//
// Every header here stays DPDK-free, so the build needs no DPDK include tree.

#include "lib/controlplane/config/cp_device.h"
#include "lib/dataplane/device/device.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/pipeline/econtext.h"

#include "lib/rust/yanet-dp-sys/shim/yanet_dp_shim.h"
