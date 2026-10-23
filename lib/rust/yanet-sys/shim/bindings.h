// Headers the SDK binds through bindgen.
//
// The same repository headers the C dataplane compiles against; the build
// script feeds bindgen the include directories and defines of the meson
// pipeline library target.
#pragma once

#include "common/lpm.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#include <rte_mbuf_core.h>

#include "dp_shim.h"
