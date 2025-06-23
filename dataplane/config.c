#include "config.h"

#include <stdlib.h>
#include <yaml.h>

#include "common/strutils.h"

enum state {
	state_empty,
	state_dataplane,
	state_dataplane_storage,
	state_dataplane_dpdk_memory,

	state_instances,
	state_instance,
	state_instance_numa_id,
	state_instance_dp_memory,
	state_instance_cp_memory,

	state_devices,
	state_device,
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

	state_connections,
	state_connection,
	state_connection_src,
	state_connection_dst,

	state_loglevel,
};

int
dataplane_config_init(FILE *file, struct dataplane_config **config) {
	enum state state = state_empty;

	yaml_parser_t parser;
	yaml_event_t event;
	if (!yaml_parser_initialize(&parser))
		return -1;

	yaml_parser_set_input_file(&parser, file);

	struct dataplane_config *dataplane =
		(struct dataplane_config *)malloc(sizeof(struct dataplane_config
		));
	if (dataplane == NULL)
		goto err_alloc_config;

	memset(dataplane, 0, sizeof(*dataplane));

	struct dataplane_instance_config *instance = NULL;
	struct dataplane_device_config *device = NULL;
	struct dataplane_device_worker_config *worker = NULL;
	struct dataplane_connection_config *connection = NULL;

	char *start = NULL;
	char *end = NULL;

	yaml_parser_parse(&parser, &event);
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
			end = start + event.data.scalar.length;

			switch (state) {
			case state_dataplane_storage:
				strtcpy(dataplane->storage,
					start,
					sizeof(dataplane->storage));
				state = state_dataplane;
				break;
			case state_dataplane_dpdk_memory:
				dataplane->dpdk_memory =
					strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
				state = state_dataplane;
				break;
			case state_loglevel:
				strtcpy(dataplane->loglevel,
					start,
					sizeof(dataplane->loglevel));
				state = state_dataplane;
				break;

			// handle new instance
			case state_instance_numa_id:
				instance->numa_idx = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
				state = state_instance;
				break;
			case state_instance_dp_memory:
				instance->dp_memory = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
				state = state_instance;
				break;
			case state_instance_cp_memory:
				instance->cp_memory = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
				state = state_instance;
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
				device->mtu = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
				state = state_device;
				break;
			case state_device_max_lro_packet_size:
				device->max_lro_packet_size =
					strtol(start, &end, 10);
				if (*end != '\0')
					goto error;

				state = state_device;
				break;
			case state_device_rss_hash:
				device->rss_hash = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;

				state = state_device;
				break;

			case state_worker_core_id:
				worker->core_id = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;

				state = state_worker;
				break;
			case state_worker_instance_id:
				worker->instance_id = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;

				state = state_worker;
				break;
			case state_worker_rx_queue_len:
				worker->rx_queue_len = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;

				state = state_worker;
				break;
			case state_worker_tx_queue_len:
				worker->tx_queue_len = strtol(start, &end, 10);
				if (*end != '\0')
					goto error;

				state = state_worker;
				break;

			case state_connection_src:
				connection->src_device_id =
					strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
				state = state_connection;
				break;
			case state_connection_dst:
				connection->dst_device_id =
					strtol(start, &end, 10);
				if (*end != '\0')
					goto error;
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
				} else if (!strcmp("instances", start)) {
					state = state_instances;
				} else if (!strcmp("devices", start)) {
					state = state_devices;
				} else if (!strcmp("connections", start)) {
					state = state_connections;
				} else if (!strcmp("loglevel", start)) {
					state = state_loglevel;
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
				if (!strcmp("port_name", start)) {
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
				} else {
					goto error;
				}
				break;
			case state_connection: {
				if (!strcmp("src_device_id", start)) {
					state = state_connection_src;
				} else if (!strcmp("dst_device_id", start)) {
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
			case state_connections:
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
			case state_connections:
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
				dataplane->device_count++;
				void *mem = realloc(
					dataplane->devices,
					sizeof(struct dataplane_device_config) *
						dataplane->device_count
				);
				if (mem == NULL) {
					goto error;
				}
				dataplane->devices =
					(struct dataplane_device_config *)mem;
				device = dataplane->devices +
					 dataplane->device_count - 1;
				memset(device, 0, sizeof(*device));
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

				state = state_worker;
				break;
			}
			case state_connections: {
				dataplane->connection_count++;
				void *mem = realloc(
					dataplane->connections,
					sizeof(struct
					       dataplane_connection_config
					) * dataplane->connection_count
				);
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
				state = state_devices;
				break;
			case state_worker:
				state = state_workers;
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
		yaml_parser_parse(&parser, &event);
	}
	yaml_event_delete(&event);

	yaml_parser_delete(&parser);

	*config = dataplane;

	return 0;

error:
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

	free(config->devices);
	free(config->connections);
	free(config);
}
