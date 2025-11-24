#include <stddef.h>

#include "lib/controlplane/config/cp_module.h"
#include "lib/counters/counters.h"

////////////////////////////////////////////////////////////////////////////////

struct dp_config;
struct cp_config;

////////////////////////////////////////////////////////////////////////////////

struct counter_mock {
	struct counter_storage *storage;
	char module_type[80];
	char module_name[80];
};

/// Mock of the single instance of YANET dataplane (dp_config+cp_config).
/// Supports only module configs without pipelines, network functions and packet
/// processing flow. Also, supports module-local packet processing.
struct yanet_mock {
	void *shm;
	struct dp_config *dp_config; // relative pointer
	struct cp_config *cp_config; // relative pointer

	size_t counters_count;
	struct counter_mock counters[100];
};

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_register_cp_module(
	struct yanet_mock *mock,
	struct cp_module *cp_module,
	char *module_type,
	char *module_name
);

int
yanet_mock_init(
	struct yanet_mock *mock,
	void *storage,
	size_t dp_memory,
	size_t cp_memory,
	char **module_types,
	size_t module_types_cnt
);

////////////////////////////////////////////////////////////////////////////////

struct agent *
yanet_mock_attach_agent(
	struct yanet_mock *mock, const char *agent_name, size_t memory_limit
);

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_prepare_for_cp_update(struct yanet_mock *mock);

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_free(struct yanet_mock *mock);

////////////////////////////////////////////////////////////////////////////////

struct dp_worker;
struct module_ectx;
struct packet_front;

typedef void (*packets_handler)(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

struct cp_module;

void
yanet_mock_handle_packets(
	struct yanet_mock *mock,
	struct cp_module *cp_module,
	struct packet_front *packet_front,
	packets_handler handler
);
