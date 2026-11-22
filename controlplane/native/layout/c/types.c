// The structures the native control plane's views are generated from.
//
// The file is compiled as the dataplane compiles its headers, with the
// project's arguments and DPDK's configuration, and its debug information
// keeps every structure the headers define: shmgen reads the layouts the
// views are generated from out of it, as it would out of the dataplane.

#include <rte_mbuf.h>

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/pipeline/econtext.h"

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

int
main(void) {
	return 0;
}
