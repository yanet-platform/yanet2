#include "counters.h"

#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

static int
register_one(struct dp_config *dp_config, const char *name, uint64_t size) {
	yanet_error *err = NULL;
	uint64_t rc = counter_registry_register(
		&dp_config->worker_counters, name, size, &err
	);
	if (rc == COUNTER_INVALID) {
		LOG(ERROR,
		    "failed to register '%s' counter: %s",
		    name,
		    yanet_error_message(err));
		yanet_error_free(err);
		return -1;
	}
	return 0;
}

int
worker_counters_register(struct dp_config *dp_config) {
	if (register_one(dp_config, "iterations", 1)) {
		return -1;
	}
	if (register_one(dp_config, "rx", 2)) {
		return -1;
	}
	if (register_one(dp_config, "tx", 2)) {
		return -1;
	}
	if (register_one(dp_config, "remote_rx", 2)) {
		return -1;
	}
	if (register_one(dp_config, "remote_tx", 2)) {
		return -1;
	}
	return 0;
}
