// Headers the SDK binds through bindgen.
//
// These are the same repository headers the C dataplane and the C decap
// module compile against; the build script feeds bindgen and the C shims
// the include directories and defines of the meson decap module target.
#pragma once

#include "common/lpm.h"
#include "common/memory_address.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/decap.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "modules/decap/dataplane/config.h"

#include <rte_mbuf_core.h>

#include "cp_shim.h"
#include "test_shim.h"
