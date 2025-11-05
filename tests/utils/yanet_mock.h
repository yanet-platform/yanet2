#include <stddef.h>

////////////////////////////////////////////////////////////////////////////////

struct dp_config;
struct cp_config;

/// Mock of the single instance of YANET dataplane (dp_config+cp_config).
/// Supports only module configs without pipelines, network functions and packet
/// processing flow. Also, supports module-local packet processing.
struct yanet_mock {
	void *shm;
	struct dp_config *dp_config;
	struct cp_config *cp_config;
};

////////////////////////////////////////////////////////////////////////////////

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
yanet_mock_agent_attach(
	struct yanet_mock *mock, const char *agent_name, size_t memory_limit
);

////////////////////////////////////////////////////////////////////////////////

void
yanet_mock_cp_update_prepare(struct yanet_mock *mock);

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
