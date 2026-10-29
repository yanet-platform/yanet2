// Headers the SDK binds through bindgen.
//
// The same repository headers the C dataplane compiles against; the build
// script feeds bindgen the include directories and defines of the meson
// packet library target.
#pragma once

#include "common/lpm.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/errors/errors.h"

#include <rte_mbuf_core.h>

#include "yanet_build_config.h"

#include "common_shim.h"
#include "dp_shim.h"
#include <errno.h>
