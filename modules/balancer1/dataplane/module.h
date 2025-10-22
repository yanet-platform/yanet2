#pragma once

////////////////////////////////////////////////////////////////////////////////

struct balancer_session_table;

struct balancer_module_config {
	struct balancer_session_table *sessions;
};