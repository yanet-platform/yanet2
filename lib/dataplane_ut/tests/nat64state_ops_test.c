/*
 * Behavior of the stateful-NAT64 session value ops and the TTL
 * selector: the session identity chosen at flow creation must survive
 * both in-layer refreshes and stale-layer promotions, the establishment
 * latch stays set once seen, and a zero timeout knob selects its
 * default.
 */

#include "common/test_assert.h"
#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/fwstate/nat64state.h"
#include "lib/fwstate/ops.h"

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
test_entry_ttl(void) {
	const struct nat64state_timeouts set = {
		.timeout = 90,
		.tcp_established = 7440,
		.tcp_transitory = 240,
	};
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, false, false, &set) ==
			90ull * 1000000000ull,
		"a non-established session must age on the general timeout"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_UDP, false, false, &set) ==
			90ull * 1000000000ull,
		"a UDP session must age on the general timeout"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, true, false, &set) ==
			7440ull * 1000000000ull,
		"an established TCP session must age on the established timeout"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, true, true, &set) ==
			240ull * 1000000000ull,
		"a TCP session both ends closed must age on the transitory "
		"timeout"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, false, true, &set) ==
			240ull * 1000000000ull,
		"a closed TCP session ages on the transitory timeout even if "
		"it "
		"never established"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_UDP, false, true, &set) ==
			90ull * 1000000000ull,
		"the closing bits never move a non-TCP session off the general "
		"timeout"
	);

	const struct nat64state_timeouts zero = {0};
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_UDP, false, false, &zero) ==
			(uint64_t)NAT64STATE_TIMEOUT_DEFAULT * 1000000000ull,
		"a zero timeout knob must select the default, not an expired "
		"session"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, true, false, &zero) ==
			(uint64_t)NAT64STATE_TCP_ESTABLISHED_DEFAULT *
				1000000000ull,
		"a zero established knob must select its default"
	);
	TEST_ASSERT(
		nat64state_entry_ttl(IPPROTO_TCP, true, true, &zero) ==
			(uint64_t)NAT64STATE_TCP_TRANSITORY_DEFAULT *
				1000000000ull,
		"a zero transitory knob must select its default"
	);
	return TEST_SUCCESS;
}

// The closing halves latch independently: a refresh that saw one end
// close keeps the other end's half as the session stored it.
static int
test_refresh_latches_closing_halves(void) {
	struct nat64state_value first = session_value(100, 0x01020304, 40000);
	first.tcp_established = true;

	// The lan side closes: the refresh carries lan_closed only.
	struct nat64state_value lan_fin = session_value(110, 0, 0);
	lan_fin.tcp_established = true;
	lan_fin.lan_closed = true;
	fwmap_update_value_nat64state(&first, &lan_fin, false, sizeof(first));
	TEST_ASSERT(
		first.lan_closed && !first.wan_closed,
		"one end closing must leave the other half open"
	);

	// The wan side closes later; the refresh knows nothing of the lan
	// half, which the stored entry must still supply.
	struct nat64state_value wan_fin = session_value(120, 0, 0);
	wan_fin.tcp_established = true;
	wan_fin.wan_closed = true;
	fwmap_update_value_nat64state(&first, &wan_fin, false, sizeof(first));
	TEST_ASSERT(
		first.lan_closed && first.wan_closed,
		"both closing halves must survive the second refresh"
	);
	TEST_ASSERT(
		first.tcp_established,
		"establishment must survive refreshes that omit it"
	);
	return TEST_SUCCESS;
}

// Promotion across a layer carries the same latches as an in-layer
// refresh.
static int
test_promotion_latches_closing_halves(void) {
	struct nat64state_value stale = session_value(100, 0x01020304, 40000);
	stale.tcp_established = true;
	stale.lan_closed = true;
	struct nat64state_value fresh = session_value(300, 0, 0);
	fresh.wan_closed = true;

	struct nat64state_value promoted;
	fwmap_promote_value_nat64state(
		&promoted, &fresh, &stale, sizeof(promoted)
	);
	TEST_ASSERT(
		promoted.tcp_established,
		"promotion must keep the established latch from the stale layer"
	);
	TEST_ASSERT(
		promoted.lan_closed && promoted.wan_closed,
		"promotion must OR the closing halves of both layers"
	);
	return TEST_SUCCESS;
}

static int
test_refresh_keeps_identity(void) {
	struct nat64state_value first = session_value(100, 0x01020304, 40000);

	struct nat64state_value refresh = session_value(200, 0, 0);
	refresh.tcp_established = true;

	fwmap_update_value_nat64state(&first, &refresh, false, sizeof(first));

	TEST_ASSERT(first.created_at == 100, "refresh must keep created_at");
	TEST_ASSERT(first.updated_at == 200, "refresh must take updated_at");
	TEST_ASSERT(
		first.ip4 == 0x01020304, "refresh must keep the allocation ip4"
	);
	TEST_ASSERT(
		first.port == 40000, "refresh must keep the allocation port"
	);
	// A stored identity survives refreshes whatever its bytes: even a
	// client address whose first, second and last bytes are zero but
	// whose middle is not.
	struct nat64state_value sparse_first =
		session_value(100, 0x01020304, 40000);
	memset(sparse_first.src6, 0, sizeof(sparse_first.src6));
	sparse_first.src6[2] = 1; // 0:0:1:: with zeros at 0, 1 and 15
	struct nat64state_value sparse_refresh = session_value(200, 0, 0);
	memset(sparse_refresh.src6, 0, sizeof(sparse_refresh.src6));
	sparse_refresh.src6[15] = 9;
	fwmap_update_value_nat64state(
		&sparse_first, &sparse_refresh, false, sizeof(sparse_first)
	);
	TEST_ASSERT(
		sparse_first.src6[2] == 1 && sparse_first.src6[15] == 0,
		"a sparse stored identity must be preserved, not replaced"
	);

	// The stored NAT64 prefix is session identity too: a refresh
	// carrying another prefix must not move the session under it.
	struct nat64state_value prefix_first = session_value(100, 1, 2);
	memset(prefix_first.prefix, 0x64, sizeof(prefix_first.prefix));
	struct nat64state_value prefix_refresh = session_value(200, 0, 0);
	memset(prefix_refresh.prefix, 0x00, sizeof(prefix_refresh.prefix));
	prefix_refresh.prefix[0] = 0x20;
	fwmap_update_value_nat64state(
		&prefix_first, &prefix_refresh, false, sizeof(prefix_first)
	);
	TEST_ASSERT(
		prefix_first.prefix[0] == 0x64 &&
			prefix_first.prefix[1] == 0x64,
		"the stored prefix must win over the refresh's"
	);
	return TEST_SUCCESS;
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
	return TEST_SUCCESS;
}

static int
test_promotion_keeps_identity(void) {
	struct nat64state_value stale = session_value(100, 0x01020304, 40000);
	stale.src6[0] = 0x20;

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
	return TEST_SUCCESS;
}

int
main(void) {
	struct dataplane_ut *ut =
		dataplane_ut_new(&(struct dataplane_ut_config){
			.cp_memory = 1u << 24,
			.dp_memory = 1u << 20,
			.worker_count = 1,
		});
	if (ut == NULL) {
		return 1;
	}

	int res = TEST_SUCCESS;
	if (res == TEST_SUCCESS) {
		res = test_entry_ttl();
	}
	if (res == TEST_SUCCESS) {
		res = test_refresh_keeps_identity();
	}
	if (res == TEST_SUCCESS) {
		res = test_refresh_latches_closing_halves();
	}
	if (res == TEST_SUCCESS) {
		res = test_first_write_copies();
	}
	if (res == TEST_SUCCESS) {
		res = test_promotion_keeps_identity();
	}
	if (res == TEST_SUCCESS) {
		res = test_promotion_latches_closing_halves();
	}

	dataplane_ut_free(ut);
	return res == TEST_SUCCESS ? 0 : 1;
}
