#include "common.h"
#include "logging/log.h"
#include <sys/mman.h>

#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <string.h>
#include <sys/mman.h>
#include <sys/stat.h>
#include <unistd.h>

#include "utils/utils.h"

#include "../../filter.h"

////////////////////////////////////////////////////////////////////////////////

int
filter_packets(struct common *common) {
	uint64_t rng = 128318;
	const size_t packets = 1000;
	size_t found = 0;
	size_t errors = 0;
	for (size_t i = 0; i < packets; ++i) {
		uint8_t src_ip[4];
		memset(src_ip, (rng_next(&rng) & 0xF0) + 3, 4);
		uint8_t dst_ip[4] = {(i + 1) & 0xFF, 0, 0, 0};
		uint16_t src_port = rng_next(&rng) & 0xFF;
		uint16_t dst_port = rng_next(&rng) & 0xFF;
		struct packet packet = make_packet4(
			src_ip,
			dst_ip,
			src_port,
			dst_port,
			i % 2 == 0 ? IPPROTO_TCP : IPPROTO_UDP,
			0,
			0
		);
		uint32_t *actions;
		uint32_t actions_count;
		int res = FILTER_QUERY(
			&common->filter,
			filter_sign,
			&packet,
			&actions,
			&actions_count
		);
		free_packet(&packet);
		if (res < 0) {
			LOG(ERROR,
			    "error occurred durring classification: %d",
			    res);
			++errors;
		} else if (actions_count > 0) {
			++found;
		}
	}
	LOG(INFO,
	    "%lu/%lu packets found (%.2lf%%)",
	    found,
	    packets,
	    100.0 * (double)found / packets);
	if (errors > 0) {
		LOG(ERROR, "%lu errors occurred during classification", errors);
		return 1;
	} else {
		return 0;
	}
}

int
main(int argc, char **argv) {
	if (argc < 3) {
		printf("Usage: %s <SHM_PATH> <SHM_SIZE>\n", argv[0]);
		return 1;
	}

	// Enable logging
	log_enable_name("trace");

	errno = 0;
	size_t size = atoi(argv[2]);
	if (errno != 0) {
		LOG(ERROR,
		    "atoi: failed to parse shared memory size: %s",
		    strerror(errno));
		return 1;
	}

	if (size < sizeof(struct common)) {
		LOG(ERROR,
		    "shared memory size if %lu, required size is at least "
		    "%lu\n",
		    size,
		    sizeof(struct common));
		return 1;
	}

	LOG(INFO, "attaching to shared memory (size=%lu)...", size);

	// Open shared memory file descriptor
	const char *shm_name_arg = argv[1];
	char shm_name_buf[NAME_MAX + 2];
	const char *shm_name;
	if (shm_name_arg[0] == '/') {
		shm_name = shm_name_arg;
	} else {
		int n = snprintf(
			shm_name_buf, sizeof(shm_name_buf), "/%s", shm_name_arg
		);
		if (n < 0 || (size_t)n >= sizeof(shm_name_buf)) {
			LOG(ERROR, "shared memory name too long");
			return 1;
		}
		shm_name = shm_name_buf;
	}
	int shm_fd = shm_open(shm_name, O_RDWR, 0);
	if (shm_fd == -1) {
		LOG(ERROR,
		    "shm_open('%s') failed: %s",
		    shm_name,
		    strerror(errno));
		return 1;
	}

	// MMap to the shared memory
	void *memory =
		mmap(NULL, size, PROT_READ | PROT_WRITE, MAP_SHARED, shm_fd, 0);
	if (memory == MAP_FAILED) {
		LOG(ERROR, "failed to mmap: %s", strerror(errno));
		close(shm_fd);
		return 1;
	}

	// Get pointer to common
	struct common *common = (struct common *)memory;

	LOG(INFO, "successfully attached to the shared memory (%p)", memory);

	// Wait for compiler to finish (with timeout)
	LOG(INFO, "waiting for compiler to finish...");
	int timeout_ms = 15000;
	int wait_interval_ms = 10;
	int elapsed_ms = 0;
	while (atomic_load(&common->ready) == 0) {
		usleep(wait_interval_ms * 1000);
		elapsed_ms += wait_interval_ms;
		if (elapsed_ms >= timeout_ms) {
			LOG(ERROR, "timeout waiting for compiler to finish");
			return 1;
		}
	}
	LOG(INFO, "compiler finished, proceeding with filtering");

	// Query packets

	LOG(INFO, "running filter packets routine...");

	if (filter_packets(common) < 0) {
		LOG(ERROR, "failed to filter packets: %s", strerror(errno));
		return 1;
	}

	// Success

	LOG(INFO, "successfully run routine");

	// Unlink shared memory

	// if (shm_unlink(shm_name) < 0) {
	//     LOG(ERROR, "Failed to unlink shared memory: %s",
	//     strerror(errno)); return 1;
	// }

	return 0;
}