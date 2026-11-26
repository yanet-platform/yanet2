// The structures the native control plane's views are generated from.
//
// The file is compiled with the project's arguments, and its debug
// information keeps every structure the headers define: shmgen reads the
// layouts the views are generated from out of it, as it would out of the
// dataplane. The structures reach DPDK through pointers only, which the
// views keep opaque, so DPDK's own headers take no part in the layouts.

#include "common/lpm.h"
#include "common/value.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "modules/decap/dataplane/config.h"

// The versions a native attach expects, which become typed Go constants.
//
// Enumerators ride the debug information the views are read from, so the
// constants come from the same compilation as the layouts. The compiler
// sizes each enumeration to hold its value, which gives the constant its
// width. Each constant is named after its enumerator less the
// enumeration's prefix: dp_config_ready_magic becomes DpConfigReadyMagic,
// module_ABI_version becomes ModuleABIVersion.
enum native_layout_u64 {
	native_layout_dp_config_ready_magic = DP_CONFIG_READY_MAGIC,
};

enum native_layout_u32 {
	native_layout_module_ABI_version = YANET_MODULE_ABI_VERSION,
};

// The block allocator's geometry, which the native allocator must share
// with the C build it shares memory with.
enum native_layout_allocator {
	native_layout_memory_block_allocator_exp = MEMORY_BLOCK_ALLOCATOR_EXP,
	native_layout_memory_block_allocator_min_bits =
		MEMORY_BLOCK_ALLOCATOR_MIN_BITS,
	native_layout_memory_block_allocator_max_align =
		MEMORY_BLOCK_ALLOCATOR_MAX_ALIGN,
	native_layout_memory_block_allocator_max_size =
		MEMORY_BLOCK_ALLOCATOR_MAX_SIZE,
	native_layout_asan_red_zone = ASAN_RED_ZONE,
};

int
main(void) {
	return 0;
}
