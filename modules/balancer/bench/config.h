#pragma once

#include <stddef.h>

struct balancer_config;

struct bench_config {
    size_t workers;
    size_t memory;
};