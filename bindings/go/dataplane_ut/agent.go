package dataplaneut

/*
#include <stdint.h>

#include "lib/controlplane/agent/agent.h"

// dataplane_ut_agent_loaded_entity_count reads the agent's count of loaded
// entity references, which the control plane updates atomically.
static uint64_t
dataplane_ut_agent_loaded_entity_count(struct agent *agent) {
	return __atomic_load_n(&agent->loaded_entity_count, __ATOMIC_ACQUIRE);
}
*/
import "C"

import "github.com/yanet-platform/yanet2/controlplane/ffi"

// LoadedEntityCount returns how many references to the agent's entities live
// generations hold, plus the destruction holds of frees in flight.
//
// An inspection for tests: the count is zero while the agent has published
// nothing and once every generation that referenced its entities, and every
// free that started on them, is gone.
func LoadedEntityCount(agent *ffi.Agent) uint64 {
	return uint64(C.dataplane_ut_agent_loaded_entity_count((*C.struct_agent)(agent.AsRawPtr())))
}
