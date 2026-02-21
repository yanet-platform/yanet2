#pragma once

#include "api/vs.h"
#include "handler.h"

////////////////////////////////////////////////////////////////////////////////

// TODO: docs
struct reals_usage {
    uint64_t counters_usage;
    uint64_t data_usage;
    uint64_t total_usage;
};

// TODO: docs
struct vs_inspect {
    uint64_t acl_usage;
    uint64_t ring_usage;
    uint64_t counters_usage;
    struct reals_usage reals_usage;
    uint64_t other_usage;
    uint64_t total_usage;
};

// TODO: docs
struct named_vs_inspect {
    struct vs_identifier identifier;
    struct vs_inspect inspect;
};

// TODO: docs
struct packet_handler_vs_inspect {
    uint64_t matcher_usage;

    uint64_t summary_vs_usage;
    
    size_t vs_count;
    struct named_vs_inspect *vs_inspects;

    uint64_t other_usage;

    uint64_t total_usage;
};

// TODO: docs
struct packet_handler_inspect {
    struct packet_handler_vs_inspect vs_ipv4_inspect;
    struct packet_handler_vs_inspect vs_ipv6_inspect;

    uint64_t counters_usage;

    uint64_t other_usage;

    uint64_t total_usage;
};

void
packet_handler_inspect(
    struct packet_handler *handler,
    struct packet_handler_inspect *inspect
);