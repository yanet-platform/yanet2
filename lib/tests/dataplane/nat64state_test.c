// Merge semantics of the stateful-NAT64 session value ops and the TTL
// selector: the session identity chosen at flow creation must survive both
// in-layer refreshes and stale-layer promotions, and the per-protocol
// timeouts must pick their branch by protocol and flags.

#include <stdio.h>
#include <string.h>

#include "lib/fwstate/nat64state.h"
#include "lib/fwstate/ops.h"

#include "common/test_assert.h"

static struct nat64state_value
session_value(uint64_t now, uint32_t ip4, uint16_t port) {
	struct nat64state_value v = {0};
	v.created_at = now;
	v.updated_at = now;
	v.last_ttl = 42;
	v.ip4 = ip4;
	v.port = port;
	return v;
}

static int
test_refresh_keeps_identity(void) {
	struct nat64state_value first = session_value(100, 0x01020304, 40000);
	first.tcp_flags = FWSTATE_SYN;

	struct nat64state_value refresh = session_value(200, 0, 0);
	refresh.tcp_flags = FWSTATE_ACK;

	fwmap_update_value_nat64state(&first, &refresh, false, sizeof(first));

	TEST_ASSERT(first.created_at == 100, "refresh must keep created_at");
	TEST_ASSERT(first.updated_at == 200, "refresh must take updated_at");
	TEST_ASSERT(
		first.ip4 == 0x01020304, "refresh must keep the allocation ip4"
	);
	TEST_ASSERT(
		first.port == 40000, "refresh must keep the allocation port"
	);
	TEST_ASSERT(
		first.tcp_flags == (FWSTATE_SYN | FWSTATE_ACK),
		"refresh must accumulate tcp flags"
	);
	return 0;
}

static int
test_first_write_copies(void) {
	struct nat64state_value slot;
	struct nat64state_value fresh = session_value(10, 0x0a000001, 1024);
	fresh.src6[15] = 1;

	fwmap_update_value_nat64state(&slot, &fresh, true, sizeof(slot));

	TEST_ASSERT(slot.created_at == 10, "first write must copy the value");
	TEST_ASSERT(slot.port == 1024, "first write must copy the allocation");
	TEST_ASSERT(slot.src6[15] == 1, "first write must copy src6");
	return 0;
	return 0;
}

static int
test_promotion_keeps_identity(void) {
	struct nat64state_value stale = session_value(100, 0x01020304, 40000);
	stale.src6[0] = 0x20;
	stale.tcp_flags = FWSTATE_SYN;

	struct nat64state_value fresh = session_value(300, 0, 0);

	struct nat64state_value promoted;
	fwmap_promote_value_nat64state(
		&promoted, &fresh, &stale, sizeof(promoted)
	);

	TEST_ASSERT(
		promoted.created_at == 100, "promotion must keep created_at"
	);
	TEST_ASSERT(
		promoted.updated_at == 300, "promotion must take updated_at"
	);
	TEST_ASSERT(
		promoted.ip4 == 0x01020304, "promotion must keep the allocation"
	);
	TEST_ASSERT(promoted.src6[0] == 0x20, "promotion must keep src6");
	TEST_ASSERT(
		promoted.tcp_flags == FWSTATE_SYN,
		"promotion must merge tcp flags"
	);
	return 0;
}

static int
test_entry_ttl(void) {
	const struct nat64state_timeouts t = {
		.tcp_syn = 10,
		.tcp_ack = 3600,
		.tcp_fin = 30,
		.udp = 120,
		.icmp = 60,
		.other = 90,
	};

	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, FWSTATE_SYN, &t) ==
			10ull * 1000000000ull,
		"a SYN without ACK must age on tcp_syn"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(
			IPPROTO_TCP, FWSTATE_SYN | FWSTATE_ACK, &t
		) == 3600ull * 1000000000ull,
		"an established flow must age on tcp_ack"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, FWSTATE_FIN, &t) ==
			30ull * 1000000000ull,
		"a FIN-seen flow must age on tcp_fin"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_UDP, 0, &t) ==
			120ull * 1000000000ull,
		"UDP must age on udp"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_ICMP, 0, &t) ==
			60ull * 1000000000ull,
		"ICMP must age on icmp"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_ESP, 0, &t) ==
			90ull * 1000000000ull,
		"other protocols must age on other"
	);
	return 0;
}

int
main(void) {
	if (test_refresh_keeps_identity() != 0) {
		return TEST_FAILED;
	}
	if (test_first_write_copies() != 0) {
		return TEST_FAILED;
	}
	if (test_promotion_keeps_identity() != 0) {
		return TEST_FAILED;
	}
	if (test_entry_ttl() != 0) {
		return TEST_FAILED;
	}
	return 0;
}
