#include "counter_storage.h"

#include <string.h>

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
	(void)cp_config;

	yanet_error *err = NULL;
	if (counter_registry_link(&dp_config->worker_counters, NULL, &err)) {
		LOG(ERROR,
		    "failed to link worker counter registry: %s",
		    yanet_error_message(err));
		yanet_error_free(err);
		return -1;
	}

	struct counter_storage **storages =
		(struct counter_storage **)memory_balloc(
			&dp_config->memory_context,
			sizeof(struct counter_storage *) * worker_count
		);
	if (storages == NULL && worker_count > 0) {
		LOG(ERROR, "failed to allocate worker counter storage array");
		return -1;
	}
	memset(storages, 0, sizeof(struct counter_storage *) * worker_count);

	for (uint64_t worker_idx = 0; worker_idx < worker_count; ++worker_idx) {
		struct counter_storage *storage = counter_storage_spawn(
			&dp_config->memory_context,
			NULL,
			&dp_config->worker_counters
		);
		if (storage == NULL) {
			LOG(ERROR,
			    "failed to spawn worker counter storage for worker "
			    "%lu",
			    worker_idx);
			for (uint64_t idx = 0; idx < worker_idx; ++idx) {
				counter_storage_free(ADDR_OF(storages + idx));
			}
			memory_bfree(
				&dp_config->memory_context,
				storages,
				sizeof(struct counter_storage *) * worker_count
			);
			return -1;
		}
		SET_OFFSET_OF(storages + worker_idx, storage);
	}

	SET_OFFSET_OF(&dp_config->worker_counter_storages, storages);
	dp_config->worker_counter_storage_count = worker_count;

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
				&dp_config->memory_context, NULL, &pc->registry
			);
		if (port_counter_storage == NULL) {
			LOG(ERROR, "failed to allocate port counter storage");
			return -1;
		}

		SET_OFFSET_OF(&pc->storage, port_counter_storage);
	}

	return 0;
}
