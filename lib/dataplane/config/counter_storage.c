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

	SET_OFFSET_OF(
		&dp_config->worker_counter_storage,
		counter_storage_spawn(
			&dp_config->memory_context,
			&dp_config->counter_storage_allocator,
			NULL,
			&dp_config->worker_counters
		)
	);

	return 0;
}
