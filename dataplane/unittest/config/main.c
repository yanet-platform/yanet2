#include "assert.h"
#include "config.h"

#include "config.h"

#include <fcntl.h>

void
check_instance(
	struct dataplane_instance_config *config,
	uint16_t numa_idx,
	uint64_t dp_memory,
	uint64_t cp_memory
) {
	assert(config->numa_idx == numa_idx);
	assert(config->dp_memory == dp_memory);
	assert(config->cp_memory == cp_memory);
}

int
main(int argc, char **argv) {
	(void)argc;
	(void)argv;

	FILE *dataplane_config_file = fopen(CONFIG_PATH, "r");

	struct dataplane_config *config = NULL;

	int init_result = dataplane_config_init(dataplane_config_file, &config);
	assert(init_result == 0);

	assert(config->instance_count == 3);
	check_instance(config->instances, 0, 1024, 2048);
	check_instance(config->instances + 1, 1, 512, 128);
	check_instance(config->instances + 2, 0, 123, 124);

	dataplane_config_free(config);
	return 0;
}