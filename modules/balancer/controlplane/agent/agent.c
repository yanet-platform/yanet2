#include "agent.h"
#include "api/agent.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "controlplane/agent/agent.h"
#include "controlplane/diag/diag.h"
#include "modules/balancer/controlplane/api/balancer.h"
#include <assert.h>
#include <stdlib.h>
#include <string.h>

static const char *agent_name = "balancer";
static const char *storage_name = "balancer_storage";

struct balancer_agent_storage {
	size_t count;
	struct balancer_agent_balancer **balancers;
};

struct balancer_agent *
balancer_agent(struct yanet_shm *shm, size_t memory) {
	struct agent *agent = agent_reattach(shm, 0, agent_name, memory);
	if (agent == NULL) {
		PUSH_ERROR("failed to reattach balancer agent");
		return NULL;
	}
	if (agent_storage_read(agent, storage_name) == NULL) {
		struct balancer_agent_storage storage;
		memset(&storage, 0, sizeof(storage));
		if (agent_storage_put(agent, storage_name, &storage, sizeof(storage)) != 0) {
			PUSH_ERROR("failed to allocate balancer storage");
			agent_cleanup(agent);
			return NULL;
		}
	}
	return (struct balancer_agent *)agent;
}

// Read config from the agent storage
extern void
clone_balancer_with_relative_pointers(
	struct balancer_agent_balancer *dst, 
	struct balancer_agent_balancer *src
);

void
balancer_agent_balancers(
	struct balancer_agent *agent, struct balancer_agent_balancers *balancers
) {
	 memset(balancers, 0, sizeof(struct balancer_agent_balancers));

	 struct balancer_agent_storage *storage = agent_storage_read((struct agent *)agent, storage_name);
	 assert(storage != NULL);
	 balancers->count = storage->count;
	 balancers->balancers = calloc(balancers->count, sizeof(struct balancer_agent_balancer));
	 
	 struct balancer_agent_balancer **agent_balancers = ADDR_OF(&storage->balancers);
	 for (size_t i = 0; i < balancers->count; ++i) {
		struct balancer_agent_balancer *agent_balancer = ADDR_OF(agent_balancers + i);
		struct balancer_agent_balancer *to_user = &balancers->balancers[i];
		clone_balancer_with_relative_pointers(to_user, agent_balancer);
	 }
}

// Free balancers with normal pointers
extern void
balancer_agent_balancers_free(struct balancer_agent_balancers *balancers);

extern void
balancer_agent_balancer_free(struct balancer_agent_balancer *balancer);

// Write config into agent storage
extern int
clone_into_balancer_cfg_with_relative_pointers(
	struct balancer_agent_balancer_config *dst, 
	struct balancer_agent_balancer_config *src,
	struct memory_context *mctx
);

extern void
free_balancer_cfg_with_relative_pointers(struct balancer_agent_balancer_config *balancer, struct memory_context *mctx);

void
free_balancer_with_relative_pointers(struct balancer_agent_balancer *balancer, struct memory_context *mctx) {
	free_balancer_cfg_with_relative_pointers(&balancer->config, mctx);
	memory_bfree(mctx, balancer, sizeof(struct balancer_agent_balancer));
}

const char *
balancer_agent_take_error_msg(struct balancer_agent *agent) {
	return agent_take_error((struct agent *)agent);
}

int
balancer_agent_update_balancer(
	struct balancer_agent *agent,
	struct balancer_agent_balancer_config *config,
	struct balancer_agent_balancer *balancer
) {
	struct agent *yanet_agent = (struct agent *)agent;
	struct memory_context *mctx = &yanet_agent->memory_context;
	
	// first, try to find balancer in the storage
	struct balancer_agent_storage *storage = agent_storage_read(yanet_agent, storage_name);
	assert(storage != NULL);

	struct balancer_agent_balancer *new_balancer = memory_balloc(mctx, sizeof(struct balancer_agent_balancer));
	if (clone_into_balancer_cfg_with_relative_pointers(&new_balancer->config, config, mctx) != 0) {
		PUSH_ERROR("failed to allocate memory to balancer config");
		return -1;
	}

	struct balancer_agent_balancer **balancers = ADDR_OF(&storage->balancers);

	for (size_t i = 0; i < storage->count; ++i) {
		struct balancer_agent_balancer *agent_balancer = ADDR_OF(balancers + i);
		if (strncmp(agent_balancer->config.balancer_name, config->balancer_name, 80) == 0) { // found balancer
			SET_OFFSET_OF(&new_balancer->handle, ADDR_OF(&agent_balancer->handle));

			free_balancer_with_relative_pointers(agent_balancer, mctx);

			SET_OFFSET_OF(balancers + i, new_balancer);
			clone_balancer_with_relative_pointers(balancer, new_balancer);

			return 0;
		}
	}

	// create new balancer
	struct balancer_handle *balancer_handle = balancer_create(yanet_agent, config->balancer_name, &config->balancer_config);
	if (balancer_handle == NULL) {
		PUSH_ERROR("failed to create new balancer");
		return -1;
	}

	SET_OFFSET_OF(&new_balancer->handle, balancer_handle);

	// now, new balancer is fully configured.

	struct balancer_agent_balancer **new_balancers = memory_balloc(mctx, sizeof(struct balancer_agent_balancer *) * (storage->count + 1));
	if (new_balancers == NULL) {
		NEW_ERROR("failed to allocate memory for balancers list");
		free_balancer_with_relative_pointers(new_balancer, mctx);
		return -1;
	}

	for (size_t i = 0; i < storage->count; ++i) {
		EQUATE_OFFSET(new_balancers + i, balancers + i);
	}

	SET_OFFSET_OF(new_balancers + storage->count, new_balancer);

	memory_bfree(mctx, &storage->balancers, sizeof(struct balancer_agent_balancers *) * storage->count);
	SET_OFFSET_OF(&storage->balancers, new_balancers);

	storage->count += 1;

	return 0;
}