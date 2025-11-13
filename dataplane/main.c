#include <stdio.h>

#include "config.h"
#include "dataplane.h"
#include "logging/log.h"

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "Usage: %s CONFIG_PATH\n", argv[0]);
		return -1;
	}
	// This function initializes and enables logging
	log_enable_name("debug");

	struct dataplane_config *config;
	FILE *config_file = fopen(argv[1], "r");
	LOG(INFO, "initialize the dataplane config");
	if (dataplane_config_init(config_file, &config)) {
		LOG(ERROR, "invalid config file: %s", argv[1]);
		return -1;
	}

	log_enable_name(config->loglevel);

	struct dataplane dataplane;

	LOG(INFO, "initialize dataplane");
	// FIXME: dataplane error handling
	int rc = dataplane_init(&dataplane, argv[0], config);
	if (rc != 0) {
		LOG(ERROR, "failed to initialize dataplane");
		return -1;
	}

	LOG(INFO, "start dataplane");
	dataplane_start(&dataplane);

	// FIXME: infinite sleep effectively
	LOG(INFO, "wait dataplane");
	dataplane_stop(&dataplane);
	LOG(INFO, "dataplane is stopped");

	LOG(INFO, "deallocate dataplane");
	dataplane_config_free(config);
	return 0;
}
