#include <stdint.h>

#include "lib/dataplane/config/plugin_abi_assert.h" // IWYU pragma: keep
#include "lib/dataplane/module/module.h"

// Compiled-against ABI version this module .so was built with.
//
// The dataplane's plugin loader dlsym's this symbol and refuses to load a
// plugin whose value does not match YANET_MODULE_ABI_VERSION.
//
// This translation unit is compiled ONLY into module shared_module (.so)
// builds, never into the static built-in modules linked into the main
// dataplane binary, so multiple statically-linked built-ins never collide
// by both defining this symbol.
//
// Explicit default visibility: a module .so may compile the rest of its
// sources with -fvisibility=hidden, and this symbol must stay dlsym-able
// regardless.
__attribute__((visibility("default"))) uint32_t yanet_module_abi_version =
	YANET_MODULE_ABI_VERSION;
