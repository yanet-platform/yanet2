#include "counter_storage.h"

#include "common/memory_address.h"
#include "lib/controlplane/config/zone.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

int
dp_counter_storage_init(
	struct dp_config *dp_config,
	struct cp_config *cp_config,
	uint64_t worker_count
) {
	counter_storage_allocator_init(
		&dp_config->counter_storage_allocator,
		&dp_config->memory_context,
		worker_count
	);
	counter_storage_allocator_init(
		&cp_config->counter_storage_allocator,
		&cp_config->memory_context,
		worker_count
	);

	yanet_error *err = NULL;
	if (counter_registry_link(&dp_config->worker_counters, NULL, &err)) {
		LOG(ERROR,
		    "failed to link worker counter registry: %s",
		    yanet_error_message(err));
		yanet_error_free(err);
		return -1;
	}

	struct counter_storage *worker_counter_storage = counter_storage_spawn(
		&dp_config->memory_context,
		&dp_config->counter_storage_allocator,
		NULL,
		&dp_config->worker_counters
	);
	if (worker_counter_storage == NULL) {
		LOG(ERROR, "failed to allocate worker counter storage");
		return -1;
	}

	SET_OFFSET_OF(
		&dp_config->worker_counter_storage, worker_counter_storage
	);

	counter_storage_allocator_init(
		&dp_config->port_counter_storage_allocator,
		&dp_config->memory_context,
		1
	);

	struct dp_port_counters *port_counters =
		ADDR_OF(&dp_config->port_counters);
	for (uint64_t port_idx = 0; port_idx < dp_config->port_count;
	     ++port_idx) {
		struct dp_port_counters *pc = port_counters + port_idx;

		if (counter_registry_link(&pc->registry, NULL, &err)) {
			LOG(ERROR,
			    "failed to link port counter registry: %s",
			    yanet_error_message(err));
			yanet_error_free(err);
			return -1;
		}

		struct counter_storage *port_counter_storage =
			counter_storage_spawn(
				&dp_config->memory_context,
				&dp_config->port_counter_storage_allocator,
				NULL,
				&pc->registry
			);
		if (port_counter_storage == NULL) {
			LOG(ERROR, "failed to allocate port counter storage");
			return -1;
		}

		SET_OFFSET_OF(&pc->storage, port_counter_storage);
	}

	return 0;
}
