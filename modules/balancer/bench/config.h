#pragma once

#include <stddef.h>

struct balancer_config;

struct bench_config {
	size_t workers;
	size_t cp_memory;
	size_t total_memory;
};