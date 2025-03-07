#include <stdio.h>

#include "config.h"
#include "dataplane.h"

int
main(int argc, char **argv) {
	if (argc != 2) {
		fprintf(stderr, "%s", "usage: yanet-dataplane <config>");
		return -1;
	}
	struct dataplane_config *config;
	FILE *config_file = fopen(argv[1], "r");
	if (dataplane_config_init(config_file, &config)) {
		fprintf(stderr, "%s", "invalid config file!\n");
		return -1;
	}

	struct dataplane dataplane;

	// FIXME: dataplane error handling
	int rc = dataplane_init(&dataplane, argv[0], config);
	if (rc != 0) {
		return -1;
	}

	dataplane_start(&dataplane);

	// FIXME: infinite sleep effectively
	dataplane_stop(&dataplane);

	dataplane_config_free(config);
	return 0;
}
