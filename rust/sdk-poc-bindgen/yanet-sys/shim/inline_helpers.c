// Per-packet C helpers of the bitcode build, marked for inlining into Rust.
//
// The unmodified decap helper and the header parsers it calls are compiled
// here in place of their own units, with an always-inline attribute merged
// into their declarations: the link-time inliner otherwise keeps them out
// of line on cost, as calls across translation units stay in the C module.
// Only the bitcode build compiles this file; the C module and the default
// build are unaffected.
#include "lib/dataplane/packet/decap.h"
#include "lib/dataplane/packet/packet.h"

__attribute__((always_inline)) int
packet_decap(struct packet *packet);

__attribute__((always_inline)) int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset);

__attribute__((always_inline)) int
parse_ipv6_header(struct packet *packet, uint16_t *type, uint16_t *offset);

#include "lib/dataplane/packet/decap.c"
#include "lib/dataplane/packet/packet.c"
