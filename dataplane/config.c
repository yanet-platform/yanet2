#include "config.h"

#include <ctype.h>
#include <errno.h>
#include <inttypes.h>
#include <stdlib.h>
#include <yaml.h>

#include "common/strutils.h"
#include "lib/dataplane/packet/packet.h"

static void
print_scalar(const char *value, size_t length) {
	fputc('\'', stderr);
	for (size_t idx = 0; idx < length; ++idx) {
		unsigned char character = (unsigned char)value[idx];
		switch (character) {
		case '\\':
			fputs("\\\\", stderr);
			break;
		case '\'':
			fputs("\\'", stderr);
			break;
		case '\0':
			fputs("\\0", stderr);
			break;
		default:
			if (isprint(character)) {
				fputc(character, stderr);
			} else {
				fprintf(stderr, "\\x%02x", character);
			}
			break;
		}
	}
	fputc('\'', stderr);
}

static int
parse_unsigned(
	const char *field,
	const char *value,
	size_t length,
	uint64_t min,
	uint64_t max,
	uint64_t *result
) {
	char *end = NULL;
	const char *first = value;
	while (isspace((unsigned char)*first)) {
		++first;
	}

	errno = 0;
	uintmax_t parsed = strtoumax(value, &end, 10);

	if (*first == '-' || value == end || end != value + length ||
	    errno == ERANGE || parsed < min || parsed > max) {
		fprintf(stderr, "invalid %s value ", field);
		print_scalar(value, length);
		fprintf(stderr,
			" (length %zu): expected an unsigned integer in range "
			"%" PRIu64 "..%" PRIu64 "\n",
			length,
			min,
			max);
		return -1;
	}

	*result = (uint64_t)parsed;
	return 0;
}

static int
parse_bool(
	const char *field, const char *value, size_t length, uint8_t *result
) {
	if (length == 4 && !strncmp(value, "true", length)) {
		*result = 1;
		return 0;
	}
	if (length == 5 && !strncmp(value, "false", length)) {
		*result = 0;
		return 0;
	}
	fprintf(stderr, "invalid %s value ", field);
	print_scalar(value, length);
	fprintf(stderr, ": expected true or false\n");
	return -1;
}

// A memory size is bytes with an optional binary unit: "1073741824",
// "1 GiB" and "1GiB" spell the same size.
static int
parse_memory_size(
	const char *field, const char *value, size_t length, uint64_t *result
) {
	static const char *const units[] = {"KiB", "MiB", "GiB", "TiB"};

	unsigned shift = 0;
	size_t number_length = length;
	for (size_t idx = 0; idx < sizeof(units) / sizeof(units[0]); ++idx) {
		if (length >= 3 && !memcmp(value + length - 3, units[idx], 3)) {
			shift = 10 * (unsigned)(idx + 1);
			number_length = length - 3;
			break;
		}
	}
	while (shift != 0 && number_length > 0 &&
	       isspace((unsigned char)value[number_length - 1])) {
		--number_length;
	}

	char *end = NULL;
	errno = 0;
	uintmax_t number = strtoumax(value, &end, 10);
	if (number_length == 0 || !isdigit((unsigned char)*value) ||
	    end != value + number_length || errno == ERANGE ||
	    number > (UINT64_MAX >> shift)) {
		fprintf(stderr, "invalid %s value ", field);
		print_scalar(value, length);
		fprintf(stderr,
			" (length %zu): expected bytes with an optional "
			"KiB, MiB, GiB or TiB unit\n",
			length);
		return -1;
	}

	*result = (uint64_t)number << shift;
	return 0;
}

static int
resolve_connections(struct dataplane_config *config) {
	for (uint64_t conn_idx = 0; conn_idx < config->connection_count;
	     ++conn_idx) {
		struct dataplane_connection_config *conn =
			config->connections + conn_idx;

		if (conn->src_device[0] == '\0' ||
		    conn->dst_device[0] == '\0') {
			return -1;
		}

		int64_t src_id = -1, dst_id = -1;
		for (uint64_t dev_idx = 0; dev_idx < config->device_count;
		     ++dev_idx) {
			const char *name = config->devices[dev_idx].device_name;
			if (!strcmp(name, conn->src_device)) {
				if (src_id >= 0) {
					return -1;
				}
				src_id = (int64_t)dev_idx;
			}
			if (!strcmp(name, conn->dst_device)) {
				if (dst_id >= 0) {
					return -1;
				}
				dst_id = (int64_t)dev_idx;
			}
		}
		if (src_id < 0 || dst_id < 0) {
			return -1;
		}

		conn->src_device_id = (uint64_t)src_id;
		conn->device_id = (uint64_t)dst_id;
	}
	return 0;
}

static int
is_virtual_port(const char *port_name) {
	return !strncmp(port_name, "virtio_user_", strlen("virtio_user_")) ||
	       !strncmp(port_name, "net_ring_", strlen("net_ring_"));
}

int
dataplane_device_config_has_roles(const struct dataplane_device_config *device
) {
	for (uint32_t idx = 0; idx < device->worker_count; ++idx) {
		const struct dataplane_queue_role *role =
			&device->workers[idx].role;
		if (!role->rss || role->dscp != 0 || role->arp || role->nd ||
		    role->l2_control) {
			return 1;
		}
	}
	return 0;
}

// Rejects queue roles a device cannot carry.
//
// Unmatched traffic lands on queue 0 when the port hashes nothing, so
// queue 0 stays in the RSS table, and a queue out of the table must steer
// some class or it would receive nothing. An ND packet may carry any DSCP
// value and the NIC's choice between overlapping rules is undefined, so ND
// and the steered DSCP values must target the same queues.
static int
validate_device_steering(const struct dataplane_device_config *device) {
	if (!dataplane_device_config_has_roles(device)) {
		return 0;
	}
	if (is_virtual_port(device->port_name)) {
		fprintf(stderr,
			"device %s: virtio_user and ring ports have no "
			"queue roles\n",
			device->device_name);
		return -1;
	}
	if (!device->workers[0].role.rss) {
		fprintf(stderr,
			"device %s: queue 0 must stay in the RSS table\n",
			device->device_name);
		return -1;
	}

	uint64_t dscp = 0;
	int nd = 0;
	for (uint32_t idx = 0; idx < device->worker_count; ++idx) {
		const struct dataplane_queue_role *role =
			&device->workers[idx].role;
		if (!role->rss && role->dscp == 0 && !role->arp && !role->nd &&
		    !role->l2_control) {
			fprintf(stderr,
				"device %s: queue %u is out of the RSS table "
				"but steers no class\n",
				device->device_name,
				idx);
			return -1;
		}
		dscp |= role->dscp;
		nd |= role->nd;
	}

	// With ND steered, each queue lists either ND and every steered DSCP
	// value, or neither.
	for (uint32_t idx = 0; nd && idx < device->worker_count; ++idx) {
		const struct dataplane_queue_role *role =
			&device->workers[idx].role;
		if (role->dscp != (role->nd ? dscp : 0)) {
			fprintf(stderr,
				"device %s: queue %u: nd and the steered dscp "
				"values can match one packet and must go to "
				"the same queues\n",
				device->device_name,
				idx);
			return -1;
		}
	}
	return 0;
}

// Returns the index of the expanded worker on a core, or the worker count
// when the core has no worker yet.
static uint64_t
find_core_worker(
	const struct dataplane_worker_config *workers,
	uint64_t worker_count,
	uint16_t core_id
) {
	for (uint64_t wrk_idx = 0; wrk_idx < worker_count; ++wrk_idx) {
		if (workers[wrk_idx].core_id == core_id) {
			return wrk_idx;
		}
	}
	return worker_count;
}

// Expand the nested devices[].workers[] declarations into the flat
// top-level worker list.
//
// Worker i declared inside device d is an assignment to queue i of d,
// carrying the declaration's ring and pool sizing, so the queue ids stay
// the worker indices inside the device. Declarations on the same core
// merge into one worker polling all their queues, in declaration order;
// a core declared with two different instances is rejected.
static int
expand_workers(struct dataplane_config *config) {
	uint64_t declared_count = 0;
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		declared_count += config->devices[dev_idx].worker_count;
	}

	if (declared_count == 0) {
		return 0;
	}

	// Sized for the worst case of one core per declaration.
	config->workers = (struct dataplane_worker_config *)calloc(
		declared_count, sizeof(struct dataplane_worker_config)
	);
	if (config->workers == NULL) {
		return -1;
	}

	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct dataplane_device_config *device =
			config->devices + dev_idx;
		for (uint32_t idx = 0; idx < device->worker_count; ++idx) {
			struct dataplane_device_worker_config *source =
				device->workers + idx;
			uint64_t wrk_idx = find_core_worker(
				config->workers,
				config->worker_count,
				source->core_id
			);
			struct dataplane_worker_config *worker =
				config->workers + wrk_idx;

			if (wrk_idx == config->worker_count) {
				worker->core_id = source->core_id;
				worker->instance_id = source->instance_id;
				++config->worker_count;
			} else if (worker->instance_id != source->instance_id) {
				fprintf(stderr,
					"core %u is declared with instance %u "
					"and instance %u\n",
					source->core_id,
					worker->instance_id,
					source->instance_id);
				return -1;
			}

			void *mem = realloc(
				worker->assignments,
				sizeof(struct dataplane_assignment_config) *
					(worker->assignment_count + 1)
			);
			if (mem == NULL) {
				return -1;
			}
			worker->assignments =
				(struct dataplane_assignment_config *)mem;
			worker->assignments[worker->assignment_count++] =
				(struct dataplane_assignment_config){
					.device_id = (uint32_t)dev_idx,
					.queue_id = idx,
					.rx_queue_len = source->rx_queue_len,
					.tx_queue_len = source->tx_queue_len,
					.num_mbufs = source->num_mbufs,
				};
		}
	}

	return 0;
}

// Rejects queue roles the devices cannot carry, and a worker polling both
// RSS queues and steering queues.
//
// Pipes are wired per worker by role, so a worker is either a steering
// worker or a data worker. Virtual ports such as taps have no role and may
// share any core. Runs on the expanded workers.
static int
validate_steering(const struct dataplane_config *config) {
	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		if (validate_device_steering(config->devices + dev_idx) != 0) {
			return -1;
		}
	}

	for (uint64_t wrk_idx = 0; wrk_idx < config->worker_count; ++wrk_idx) {
		const struct dataplane_worker_config *worker =
			config->workers + wrk_idx;
		int rss = 0;
		int steering = 0;
		for (uint32_t idx = 0; idx < worker->assignment_count; ++idx) {
			const struct dataplane_assignment_config *assignment =
				worker->assignments + idx;
			const struct dataplane_device_config *device =
				config->devices + assignment->device_id;
			if (is_virtual_port(device->port_name)) {
				continue;
			}
			if (device->workers[assignment->queue_id].role.rss) {
				rss = 1;
			} else {
				steering = 1;
			}
		}
		if (rss && steering) {
			fprintf(stderr,
				"core %u polls both RSS queues and steering "
				"queues\n",
				worker->core_id);
			return -1;
		}
	}
	return 0;
}

enum state {
	state_empty,
	state_dataplane,
	state_dataplane_storage,
	state_dataplane_dpdk_memory,
	state_dataplane_packet_recirc_limit,
	state_dataplane_iova_mode,

	state_instances,
	state_instance,
	state_instance_numa_id,
	state_instance_dp_memory,
	state_instance_cp_memory,

	state_devices,
	state_device,
	state_device_name,
	state_device_port_name,
	state_device_mac_addr,
	state_device_mtu,
	state_device_max_lro_packet_size,
	state_device_rss_hash,

	state_workers,
	state_worker,
	state_worker_core_id,
	state_worker_instance_id,
	state_worker_rx_queue_len,
	state_worker_tx_queue_len,
	state_worker_num_mbufs,

	state_worker_rss,
	state_steer,
	state_steer_dscp,
	state_steer_flag,

	state_connections,
	state_connection,
	state_connection_src,
	state_connection_dst,

	state_loglevel,

	state_plugin_dir,
	state_modules,
	state_objects,
};

int
dataplane_config_init(FILE *file, struct dataplane_config **config) {
	enum state state = state_empty;

	yaml_parser_t parser;
	yaml_event_t event;
	int event_live = 0;
	if (!yaml_parser_initialize(&parser)) {
		return -1;
	}

	yaml_parser_set_input_file(&parser, file);

	struct dataplane_config *dataplane =
		(struct dataplane_config *)malloc(sizeof(struct dataplane_config
		));
	if (dataplane == NULL) {
		goto err_alloc_config;
	}

	memset(dataplane, 0, sizeof(*dataplane));
	dataplane->packet_recirc_limit = PACKET_RECIRC_LIMIT_DEFAULT;

	struct dataplane_instance_config *instance = NULL;
	struct dataplane_device_config *device = NULL;
	struct dataplane_device_worker_config *worker = NULL;
	// Whether the steered DSCP values come as a list rather than a single
	// value.
	int dscp_list = 0;
	// The steered class flag a boolean scalar sets, and its field name.
	uint8_t *steer_flag = NULL;
	const char *steer_flag_name = NULL;
	struct dataplane_connection_config *connection = NULL;

	char *start = NULL;
	size_t scalar_length = 0;
	uint64_t value = 0;

	if (!yaml_parser_parse(&parser, &event)) {
		goto error;
	}
	event_live = 1;
	while (event.type != YAML_STREAM_END_EVENT) {

		switch (event.type) {
		case YAML_NO_EVENT:
			break;
		case YAML_STREAM_START_EVENT:
			break;
		case YAML_STREAM_END_EVENT:
			break;
		case YAML_DOCUMENT_START_EVENT:
			break;
		case YAML_DOCUMENT_END_EVENT:
			break;

		case YAML_ALIAS_EVENT:
			break;

		case YAML_SCALAR_EVENT:
			start = (char *)event.data.scalar.value;
			scalar_length = event.data.scalar.length;

			switch (state) {
			case state_dataplane_storage:
				strtcpy(dataplane->storage,
					start,
					sizeof(dataplane->storage));
				state = state_dataplane;
				break;
			case state_dataplane_dpdk_memory:
				if (parse_unsigned(
					    "dataplane.dpdk_memory",
					    start,
					    scalar_length,
					    0,
					    UINT64_MAX,
					    &dataplane->dpdk_memory
				    ) != 0) {
					goto error;
				}
				state = state_dataplane;
				break;
			case state_dataplane_packet_recirc_limit:
				if (parse_unsigned(
					    "dataplane.packet_recirc_limit",
					    start,
					    scalar_length,
					    PACKET_RECIRC_LIMIT_MIN,
					    PACKET_RECIRC_LIMIT_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				dataplane->packet_recirc_limit =
					(uint16_t)value;
				state = state_dataplane;
				break;
			case state_dataplane_iova_mode:
				strtcpy(dataplane->iova_mode,
					start,
					sizeof(dataplane->iova_mode));
				state = state_dataplane;
				break;
			case state_loglevel:
				strtcpy(dataplane->loglevel,
					start,
					sizeof(dataplane->loglevel));
				state = state_dataplane;
				break;
			case state_plugin_dir:
				strtcpy(dataplane->plugin_dir,
					start,
					sizeof(dataplane->plugin_dir));
				state = state_dataplane;
				break;
			case state_modules:
				if (dataplane->module_count >=
				    DATAPLANE_MAX_MODULES) {
					goto error;
				}
				strtcpy(dataplane->module_names
						[dataplane->module_count],
					start,
					DATAPLANE_MODULE_NAME_LEN);
				dataplane->module_count++;
				break;
			case state_objects:
				if (dataplane->object_count >=
				    DATAPLANE_MAX_OBJECTS) {
					goto error;
				}
				if (scalar_length == 0 ||
				    scalar_length >=
					    DATAPLANE_OBJECT_NAME_LEN) {
					goto error;
				}
				strtcpy(dataplane->object_names
						[dataplane->object_count],
					start,
					DATAPLANE_OBJECT_NAME_LEN);
				dataplane->object_count++;
				break;

			// handle new instance
			case state_instance_numa_id:
				if (parse_unsigned(
					    "instances.numa_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				instance->numa_idx = (uint16_t)value;
				state = state_instance;
				break;
			case state_instance_dp_memory:
				if (parse_memory_size(
					    "instances.dp_memory",
					    start,
					    scalar_length,
					    &instance->dp_memory
				    ) != 0) {
					goto error;
				}
				state = state_instance;
				break;
			case state_instance_cp_memory:
				if (parse_memory_size(
					    "instances.cp_memory",
					    start,
					    scalar_length,
					    &instance->cp_memory
				    ) != 0) {
					goto error;
				}
				state = state_instance;
				break;
			case state_device_name:
				strtcpy(device->device_name,
					start,
					sizeof(device->device_name));
				state = state_device;
				break;

			case state_device_port_name:
				strtcpy(device->port_name,
					start,
					sizeof(device->port_name));
				state = state_device;
				break;
			case state_device_mac_addr:
				strtcpy(device->mac_addr,
					start,
					sizeof(device->mac_addr));
				state = state_device;
				break;
			case state_device_mtu:
				if (parse_unsigned(
					    "devices.mtu",
					    start,
					    scalar_length,
					    0,
					    UINT32_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				device->mtu = (uint32_t)value;
				state = state_device;
				break;
			case state_device_max_lro_packet_size:
				if (parse_unsigned(
					    "devices.max_lro_packet_size",
					    start,
					    scalar_length,
					    0,
					    UINT64_MAX,
					    &device->max_lro_packet_size
				    ) != 0) {
					goto error;
				}

				state = state_device;
				break;
			case state_device_rss_hash:
				if (parse_unsigned(
					    "devices.rss_hash",
					    start,
					    scalar_length,
					    0,
					    UINT64_MAX,
					    &device->rss_hash
				    ) != 0) {
					goto error;
				}

				state = state_device;
				break;

			case state_worker_core_id:
				if (parse_unsigned(
					    "workers.core_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->core_id = (uint16_t)value;

				state = state_worker;
				break;
			case state_worker_instance_id:
				if (parse_unsigned(
					    "workers.instance_id",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->instance_id = (uint16_t)value;

				state = state_worker;
				break;
			case state_worker_rx_queue_len:
				if (parse_unsigned(
					    "workers.rx_queue_len",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->rx_queue_len = (uint16_t)value;

				state = state_worker;
				break;
			case state_worker_tx_queue_len:
				if (parse_unsigned(
					    "workers.tx_queue_len",
					    start,
					    scalar_length,
					    0,
					    UINT16_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->tx_queue_len = (uint16_t)value;

				state = state_worker;
				break;
			case state_worker_num_mbufs:
				if (parse_unsigned(
					    "workers.num_mbufs",
					    start,
					    scalar_length,
					    0,
					    UINT32_MAX,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->num_mbufs = (uint32_t)value;

				state = state_worker;
				break;

			case state_worker_rss:
				if (parse_bool(
					    "workers.rss",
					    start,
					    scalar_length,
					    &worker->role.rss
				    ) != 0) {
					goto error;
				}
				state = state_worker;
				break;
			case state_steer_dscp:
				// DSCP 0 is all unmarked traffic, so it is
				// never steered.
				if (parse_unsigned(
					    "workers.steer.dscp",
					    start,
					    scalar_length,
					    1,
					    63,
					    &value
				    ) != 0) {
					goto error;
				}
				worker->role.dscp |= UINT64_C(1) << value;
				if (!dscp_list) {
					state = state_steer;
				}
				break;
			case state_steer_flag:
				if (parse_bool(
					    steer_flag_name,
					    start,
					    scalar_length,
					    steer_flag
				    ) != 0) {
					goto error;
				}
				state = state_steer;
				break;

			case state_connection_src:
				strtcpy(connection->src_device,
					start,
					sizeof(connection->src_device));
				state = state_connection;
				break;
			case state_connection_dst:
				strtcpy(connection->dst_device,
					start,
					sizeof(connection->dst_device));
				state = state_connection;
				break;

			case state_empty:
				if (!strcmp("dataplane", start)) {
					state = state_dataplane;
				} else {
					goto error;
				}
				break;
			case state_dataplane:
				if (!strcmp("storage", start)) {
					state = state_dataplane_storage;
				} else if (!strcmp("dpdk_memory", start)) {
					state = state_dataplane_dpdk_memory;
				} else if (!strcmp("packet_recirc_limit",
						   start)) {
					state = state_dataplane_packet_recirc_limit;
				} else if (!strcmp("iova_mode", start)) {
					state = state_dataplane_iova_mode;
				} else if (!strcmp("instances", start)) {
					state = state_instances;
				} else if (!strcmp("devices", start)) {
					state = state_devices;
				} else if (!strcmp("connections", start)) {
					state = state_connections;
				} else if (!strcmp("loglevel", start)) {
					state = state_loglevel;
				} else if (!strcmp("plugin_dir", start)) {
					state = state_plugin_dir;
				} else if (!strcmp("modules", start)) {
					state = state_modules;
				} else if (!strcmp("objects", start)) {
					state = state_objects;
				} else {
					goto error;
				}

				break;
			case state_instance:
				if (!strcmp("numa_id", start)) {
					state = state_instance_numa_id;
				} else if (!strcmp("dp_memory", start)) {
					state = state_instance_dp_memory;
				} else if (!strcmp("cp_memory", start)) {
					state = state_instance_cp_memory;
				} else {
					goto error;
				}

				break;
			case state_device:
				if (!strcmp("device_name", start)) {
					state = state_device_name;
				} else if (!strcmp("port_name", start)) {
					state = state_device_port_name;
				} else if (!strcmp("mac_addr", start)) {
					state = state_device_mac_addr;
				} else if (!strcmp("mtu", start)) {
					state = state_device_mtu;
				} else if (!strcmp("max_lro_packet_size",
						   start)) {
					state = state_device_max_lro_packet_size;
				} else if (!strcmp("rss_hash", start)) {
					state = state_device_rss_hash;
				} else if (!strcmp("workers", start)) {
					state = state_workers;
				} else {
					goto error;
				}

				break;
			case state_worker:
				if (!strcmp("core_id", start)) {
					state = state_worker_core_id;
				} else if (!strcmp("instance_id", start)) {
					state = state_worker_instance_id;
				} else if (!strcmp("rx_queue_len", start)) {
					state = state_worker_rx_queue_len;
				} else if (!strcmp("tx_queue_len", start)) {
					state = state_worker_tx_queue_len;
				} else if (!strcmp("num_mbufs", start)) {
					state = state_worker_num_mbufs;
				} else if (!strcmp("rss", start)) {
					state = state_worker_rss;
				} else if (!strcmp("steer", start)) {
					state = state_steer;
				} else {
					goto error;
				}

				break;
			case state_steer:
				if (!strcmp("dscp", start)) {
					state = state_steer_dscp;
				} else if (!strcmp("arp", start)) {
					steer_flag = &worker->role.arp;
					steer_flag_name = "workers.steer.arp";
					state = state_steer_flag;
				} else if (!strcmp("nd", start)) {
					steer_flag = &worker->role.nd;
					steer_flag_name = "workers.steer.nd";
					state = state_steer_flag;
				} else if (!strcmp("l2_control", start)) {
					steer_flag = &worker->role.l2_control;
					steer_flag_name =
						"workers.steer.l2_control";
					state = state_steer_flag;
				} else {
					goto error;
				}
				break;
			case state_connection: {
				if (!strcmp("src_device", start)) {
					state = state_connection_src;
				} else if (!strcmp("dst_device", start)) {
					state = state_connection_dst;
				} else {
					goto error;
				}
				break;
			}

			default:
				goto error;
			}

			break;

		case YAML_SEQUENCE_START_EVENT:
			switch (state) {
			case state_instances:
				break;
			case state_devices:
				break;
			case state_workers:
				break;
			case state_steer_dscp:
				dscp_list = 1;
				break;
			case state_connections:
				break;
			case state_modules:
				break;
			case state_objects:
				break;
			default:
				goto error;
			}
			break;
		case YAML_SEQUENCE_END_EVENT:
			switch (state) {
			case state_instances:
				state = state_dataplane;
				break;
			case state_devices:
				state = state_dataplane;
				break;
			case state_workers:
				state = state_device;
				break;
			case state_steer_dscp:
				dscp_list = 0;
				state = state_steer;
				break;
			case state_connections:
				state = state_dataplane;
				break;
			case state_modules:
				state = state_dataplane;
				break;
			case state_objects:
				state = state_dataplane;
				break;
			default:
				goto error;
			}
			break;

		case YAML_MAPPING_START_EVENT:
			switch (state) {
			case state_empty:
				break;
			case state_dataplane:
				break;
			case state_instances: {
				++dataplane->instance_count;

				void *mem = realloc(
					dataplane->instances,
					sizeof(struct dataplane_instance_config
					) * dataplane->instance_count
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->instances =
					(struct dataplane_instance_config *)mem;

				instance = dataplane->instances +
					   dataplane->instance_count - 1;
				memset(instance, 0, sizeof(*instance));
				state = state_instance;
				break;
			}
			case state_devices: {
				void *mem = realloc(
					dataplane->devices,
					sizeof(struct dataplane_device_config) *
						(dataplane->device_count + 1)
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->devices =
					(struct dataplane_device_config *)mem;
				device = dataplane->devices +
					 dataplane->device_count;
				memset(device, 0, sizeof(*device));
				++dataplane->device_count;
				state = state_device;
				break;
			}
			case state_workers: {
				device->worker_count++;
				void *mem = realloc(
					device->workers,
					sizeof(struct
					       dataplane_device_worker_config
					) * device->worker_count
				);
				if (mem == NULL) {
					goto error;
				}
				device->workers =
					(struct dataplane_device_worker_config
						 *)mem;
				worker = device->workers +
					 device->worker_count - 1;
				memset(worker, 0, sizeof(*worker));
				worker->role.rss = 1;

				state = state_worker;
				break;
			}
			case state_steer:
				break;
			case state_connections: {
				dataplane->connection_count++;
				void *mem = realloc(
					dataplane->connections,
					sizeof(struct
					       dataplane_connection_config
					) * dataplane->connection_count
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->connections =
					(struct dataplane_connection_config *)
						mem;
				connection = dataplane->connections +
					     dataplane->connection_count - 1;
				memset(connection, 0, sizeof(*connection));

				state = state_connection;
				break;
			}
			default:
				goto error;
			}
			break;
		case YAML_MAPPING_END_EVENT:
			switch (state) {
			case state_empty:
				break;
			case state_dataplane:
				state = state_empty;
				break;
			case state_instance:
				state = state_instances;
				break;
			case state_device:
				// Default device_name to port_name when the
				// YAML did not provide an explicit device_name.
				if (device->device_name[0] == '\0') {
					strtcpy(device->device_name,
						device->port_name,
						sizeof(device->device_name));
				}
				state = state_devices;
				break;
			case state_worker:
				state = state_workers;
				break;
			case state_steer:
				state = state_worker;
				break;
			case state_connection:
				state = state_connections;
				break;
			default:
				goto error;
			}
			break;

		default:
			break;
		}

		yaml_event_delete(&event);
		event_live = 0;
		if (!yaml_parser_parse(&parser, &event)) {
			goto error;
		}
		event_live = 1;
	}
	yaml_event_delete(&event);
	event_live = 0;

	if (resolve_connections(dataplane) != 0) {
		goto error;
	}

	if (expand_workers(dataplane) != 0) {
		goto error;
	}

	if (validate_steering(dataplane) != 0) {
		goto error;
	}

	yaml_parser_delete(&parser);

	*config = dataplane;

	return 0;

error:
	if (event_live) {
		yaml_event_delete(&event);
	}
	dataplane_config_free(dataplane);

err_alloc_config:
	yaml_parser_delete(&parser);
	return -1;
}

void
dataplane_config_free(struct dataplane_config *config) {
	free(config->instances);

	for (uint64_t dev_idx = 0; dev_idx < config->device_count; ++dev_idx) {
		struct dataplane_device_config *device =
			config->devices + dev_idx;
		free(device->workers);
	}

	for (uint64_t wrk_idx = 0; wrk_idx < config->worker_count; ++wrk_idx) {
		free(config->workers[wrk_idx].assignments);
	}

	free(config->workers);
	free(config->devices);
	free(config->connections);
	free(config);
}
