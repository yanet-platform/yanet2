#pragma once

#include <stdint.h>

struct packet_front;
struct balancer_packet_handler;
struct counter_storage;

struct worker_context {
    struct packet_front *packet_front;
    struct balancer_packet_handler *packet_handler;
    struct counter_storage *counter_storage;
    uint32_t worker_idx;

    // current time in seconds
    uint32_t now;
};