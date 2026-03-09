#include "agent.h"
#include "api/agent.h"
#include "controlplane/agent/agent.h"
#include "controlplane/diag/diag.h"
#include "manager.h"
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

////////////////////////////////////////////////////////////////////////////////

void
balancer_agent_inspect(
	struct balancer_agent *agent, struct agent_inspect *inspect
) {
	struct agent *base_agent = (struct agent *)agent;

	// Get memory context statistics
	inspect->memory_limit = base_agent->memory_limit;
	inspect->memory_usage = base_agent->memory_context.balloc_size -
				base_agent->memory_context.bfree_size;

	// Get all managers
	struct balancer_managers managers;
	balancer_agent_managers(agent, &managers);

	inspect->balancer_count = managers.count;

	if (managers.count == 0) {
		inspect->balancers = NULL;
		return;
	}

	// Allocate array for balancer inspections
	inspect->balancers =
		calloc(managers.count, sizeof(struct named_balancer_inspect));

	// Fill in each balancer inspection
	for (size_t i = 0; i < managers.count; ++i) {
		struct balancer_manager *manager = managers.managers[i];
		inspect->balancers[i].name = balancer_manager_name(manager);
		balancer_manager_inspect(
			manager, &inspect->balancers[i].inspect
		);
		inspect->memory_usage +=
			inspect->balancers[i].inspect.total_usage;
	}
}

void
balancer_agent_inspect_free(struct agent_inspect *inspect) {
	if (inspect == NULL || inspect->balancers == NULL) {
		return;
	}

	// Free each balancer inspection
	for (size_t i = 0; i < inspect->balancer_count; ++i) {
		balancer_manager_inspect_free(&inspect->balancers[i].inspect);
	}

	// Free the balancers array
	free(inspect->balancers);
	inspect->balancers = NULL;
	inspect->balancer_count = 0;
}

struct dp_config *
balancer_agent_dp_config(struct balancer_agent *agent) {
	return agent_dp_config((struct agent *)agent);
}