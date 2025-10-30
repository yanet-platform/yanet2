#include "zone.h"
#include <stdint.h>

struct dp_config *
dp_config_nextk(struct dp_config *current, uint32_t k) {
	for (uint32_t i = 0; i < k; ++i) {
		current = (struct dp_config *)((uintptr_t)current +
					       current->storage_size);
	}
	return current;
}

void
dp_config_wait_for_gen(struct dp_config *dp_config, uint64_t gen) {
	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	uint64_t idx = 0;
	do {
		volatile struct dp_worker *worker = ADDR_OF(workers + idx);
		if (worker->gen < gen) {
			// TODO cpu yield
			continue;
		}

		++idx;
	} while (idx < dp_config->worker_count);
}

bool
dp_config_try_lock(struct dp_config *dp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	return __atomic_compare_exchange_n(
		&dp_config->config_lock,
		&zero,
		pid,
		false,
		__ATOMIC_RELAXED,
		__ATOMIC_RELAXED
	);
}

void
dp_config_lock(struct dp_config *dp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	while (!__atomic_compare_exchange_n(
		&dp_config->config_lock,
		&zero,
		pid,
		false,
		__ATOMIC_RELAXED,
		__ATOMIC_RELAXED
	)) {
		zero = 0;
	};
}

bool
dp_config_unlock(struct dp_config *dp_config) {
	pid_t pid = getpid();
	pid_t zero = 0;
	return __atomic_compare_exchange_n(
		&dp_config->config_lock,
		&pid,
		zero,
		false,
		__ATOMIC_RELAXED,
		__ATOMIC_RELAXED
	);
}

int
dp_config_lookup_module(
	struct dp_config *dp_config, const char *name, uint64_t *index
) {
	struct dp_module *modules = ADDR_OF(&dp_config->dp_modules);
	for (uint64_t idx = 0; idx < dp_config->module_count; ++idx) {
		if (!strncmp(
			    modules[idx].name, name, sizeof(modules[idx].name)
		    )) {
			*index = idx;
			return 0;
		}
	}
	return -1;
}

int
dp_config_lookup_device(
	struct dp_config *dp_config, const char *name, uint64_t *index
) {
	struct dp_device *devices = ADDR_OF(&dp_config->dp_devices);
	for (uint64_t idx = 0; idx < dp_config->device_count; ++idx) {
		if (!strncmp(
			    devices[idx].name, name, sizeof(devices[idx].name)
		    )) {
			*index = idx;
			return 0;
		}
	}
	return -1;
}
