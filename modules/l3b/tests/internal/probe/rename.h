#pragma once

// Included production exports must never shadow the linked production archive.
#define l3b_session_timeouts_defaults l3b_probe_session_timeouts_defaults
#define l3b_virtual_service_create l3b_probe_service_create_impl
#define l3b_virtual_service_free l3b_probe_service_free
#define l3b_virtual_service_counter_read l3b_probe_service_counter_read
#define l3b_virtual_service_session_table l3b_probe_service_session_table
#define l3b_virtual_service_update_ring l3b_probe_service_update_ring
#define l3b_virtual_service_set_real_server_state                              \
	l3b_probe_service_set_real_state
#define new_object_l3b_virtual_service l3b_probe_new_service_object
#define l3b_module_config_new l3b_probe_module_new
#define l3b_module_config_update l3b_probe_module_update_impl
#define l3b_module_config_free l3b_probe_module_free
#define classify_join l3b_probe_classify_join
#define classify_join_compact l3b_probe_classify_join_compact
#define classify_coverage_map l3b_probe_classify_coverage_map
#define classify_decode l3b_probe_classify_decode
#define classify_join_rules l3b_probe_classify_join_rules
