#include "agent.h"
#include "api/agent.h"
#include "controlplane/agent/agent.h"
#include "controlplane/diag/diag.h"
#include "modules/balancer/controlplane/api/balancer.h"
#include <assert.h>
#include <stdlib.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

const char *agent_name = "balancer";
const char *storage_name = "balancer_storage";

////////////////////////////////////////////////////////////////////////////////

struct balancer_agent *
balancer_agent(struct yanet_shm *shm, size_t memory) {
	struct agent *agent = agent_reattach(shm, 0, agent_name, memory);
	if (agent == NULL) {
		PUSH_ERROR("failed to reattach balancer agent");
		return NULL;
	}

	if (agent_storage_read(agent, storage_name) == NULL) {
		struct balancer_managers managers;
		memset(&managers, 0, sizeof(managers));
		if (agent_storage_put(
			    agent, storage_name, &managers, sizeof(managers)
		    ) != 0) {
			PUSH_ERROR("failed to allocate balancer storage");
			agent_cleanup(agent);
			return NULL;
		}
	}

	return (struct balancer_agent *)agent;
}

const char *
balancer_agent_take_error(struct balancer_agent *agent) {
	return agent_take_error((struct agent *)agent);
}
