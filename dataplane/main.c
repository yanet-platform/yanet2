#include <getopt.h>
#include <stdio.h>

#include <rte_version.h>

#include "config.h"
#include "dataplane.h"
#include "logging/log.h"
#include "yanet_build_config.h"

static void
print_version(void) {
	printf("yanet-dataplane %s\n", YANET_VERSION);
	printf("  Compiler:   %s %s\n",
	       YANET_COMPILER_ID,
	       YANET_COMPILER_VERSION);
	printf("  Build type: %s\n", YANET_BUILD_TYPE);
	printf("  Built:      %s\n", YANET_BUILD_DATE);
	printf("  Git commit: %s\n", YANET_GIT_COMMIT);
	printf("  DPDK:       %s\n", rte_version());
}

static void
print_usage(const char *prog) {
	printf("Usage: %s [OPTIONS] [CONFIG_PATH]\n", prog);
	printf("Options:\n");
	printf("  -c, --config PATH   Path to the configuration file\n");
	printf("  -v, --version       Print version information and exit\n");
	printf("  -h, --help          Print this help message and exit\n");
	printf("\n");
	printf("The configuration file path may be passed either via the\n"
	       "-c/--config option or as a positional CONFIG_PATH argument\n"
	       "(kept for backward compatibility).\n");
}

int
main(int argc, char **argv) {
	static struct option long_options[] = {
		{"config", required_argument, 0, 'c'},
		{"version", no_argument, 0, 'v'},
		{"help", no_argument, 0, 'h'},
		{0, 0, 0, 0}
	};

	const char *config_path = NULL;

	int opt;
	while ((opt = getopt_long(argc, argv, "c:vh", long_options, NULL)) != -1
	) {
		switch (opt) {
		case 'c':
			config_path = optarg;
			break;
		case 'v':
			print_version();
			return 0;
		case 'h':
			print_usage(argv[0]);
			return 0;
		default:
			print_usage(argv[0]);
			return -1;
		}
	}

	// Fall back to the positional CONFIG_PATH argument for backward
	// compatibility when -c/--config is not provided.
	if (config_path == NULL) {
		if (optind < argc) {
			config_path = argv[optind];
		} else {
			fprintf(stderr,
				"Error: configuration file path is required "
				"(use -c/--config or pass it as a positional "
				"argument)\n");
			print_usage(argv[0]);
			return -1;
		}
	}

	// This function initializes and enables logging
	log_enable_name("debug");

	struct dataplane_config *config;
	FILE *config_file = fopen(config_path, "r");
	if (config_file == NULL) {
		LOG(ERROR, "failed to open config file: %s", config_path);
		return -1;
	}
	LOG(INFO, "initialize the dataplane config");
	int rc = dataplane_config_init(config_file, &config);
	fclose(config_file);
	if (rc) {
		LOG(ERROR, "invalid config file: %s", config_path);
		return -1;
	}

	log_enable_name(config->loglevel);

	struct dataplane dataplane;

	LOG(INFO, "initialize dataplane");
	// FIXME: dataplane error handling
	rc = dataplane_init(&dataplane, argv[0], config);
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
