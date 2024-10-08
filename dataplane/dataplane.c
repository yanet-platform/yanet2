#include "dataplane.h"

#include <stdint.h>

struct eal_config {
	const char *binary;
	uint32_t device_count;
	const char *device_addrs;
	uint32_t main_core_id;
	uint32_t worker_count;
	const uint32_t *worker_core_ids;
};

int
dpdk_init(const char *binary)
{
	(void) binary;
/*
	unsigned int eal_argc = 0;
	char* eal_argv[128];
#define insert_eal_arg(args...)                                                                               \
	do                                                                                                    \
	{                                                                                                     \
		eal_argv[eal_argc++] = &buffer[bufferPosition];                                               \
		bufferPosition += snprintf(&buffer[bufferPosition], sizeof(buffer) - bufferPosition, ##args); \
		bufferPosition++;                                                                             \
	} while (0)


	insert_eal_arg("%s", binary);

	//FIXME use huge pages if configured
	insert_eal_arg("--no-huge");

	insert_eal_arg("--proc-type=primary");


	eal_argv[eal_argc] = nullptr;

	return rte_eal_init(eal_argc, eal_argv);
	*/

	return 0;
}

void
dataplane_init()
{


}
