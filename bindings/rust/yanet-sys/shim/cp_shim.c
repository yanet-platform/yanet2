#include "cp_shim.h"

#include "common/memory_address.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"

struct cp_config_gen *
yanet_sys_cp_gen_acquire(struct agent *agent) {
	return cp_config_gen_acquire(ADDR_OF(&agent->cp_config));
}

void
yanet_sys_cp_gen_release(
	struct agent *agent, struct cp_config_gen *config_gen
) {
	cp_config_gen_release(ADDR_OF(&agent->cp_config), config_gen);
}
