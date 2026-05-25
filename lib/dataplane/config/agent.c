#include "agent.h"

#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/strutils.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/zone.h"

struct agent *
dp_system_agent_new(
	struct cp_config *cp_config,
	struct dp_config *dp_config,
	const char *name
) {
	struct agent *agent = (struct agent *)memory_balloc(
		&cp_config->memory_context, sizeof(struct agent)
	);
	if (agent == NULL) {
		return NULL;
	}

	memset(agent, 0, sizeof(struct agent));
	strtcpy(agent->name, name, sizeof(agent->name));
	memory_context_init_from(
		&agent->memory_context, &cp_config->memory_context, name
	);
	SET_OFFSET_OF(&agent->dp_config, dp_config);
	SET_OFFSET_OF(&agent->cp_config, cp_config);
	return agent;
}
