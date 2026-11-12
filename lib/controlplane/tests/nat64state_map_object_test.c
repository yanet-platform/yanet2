/*
 * Tests for the nat64state map objects: the lan and wan families create
 * real fwtables under their own cp_object, link per worker like the v4/v6
 * families, and write sessions whose identity survives refreshes through
 * the nat64state value ops.
 */

#include "api/agent.h"

#include "common/memory.h"
#include "common/memory_block.h"
#include "common/test_assert.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_object.h"
#include "lib/controlplane/config/zone.h"

#include "objects/fwstate/api/fwstate_map_nat64_object.h"

#include "lib/dataplane_ut/dataplane_ut.h"
#include "lib/errors/errors.h"

#include "lib/logging/log.h"

#include <errno.h>
#include <stdio.h>
#include <string.h>

#define NAT64STATE_TEST_MEMORY_LIMIT (2u * 1024u * 1024u)

static struct nat64state_lan_key
lan_key(uint16_t proto, uint16_t sport, uint16_t dport) {
	struct nat64state_lan_key key = {0};
	key.hdr.proto = proto;
	key.hdr.src_port = sport;
	key.hdr.dst_port = dport;
	memset(key.src_addr, 0x20, sizeof(key.src_addr));
	memset(key.dst_addr, 0x40, sizeof(key.dst_addr));
	return key;
}

static struct nat64state_wan_key
wan_key(uint16_t proto, uint16_t lport, uint16_t rport) {
	struct nat64state_wan_key key = {0};
	key.hdr.proto = proto;
	key.hdr.src_port = lport;
	key.hdr.dst_port = rport;
	key.local_addr = 0x01020304;
	key.remote_addr = 0x05060708;
	return key;
}

// The lan object creates a keyed table: an inserted session is found by
// its lan key, and a refresh through a racing first packet on another
// worker keeps the allocation the first writer chose.
static int
test_lan_session_identity(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "nat64state-lan", NAT64STATE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_object *cp =
		fwstate_map_nat64_object_config_new(agent, "nat0", &err);
	TEST_ASSERT_NOT_NULL(cp, "fwstate_map_nat64_object_config_new failed");

	struct fwstate_map_nat64_object *obj =
		container_of(cp, struct fwstate_map_nat64_object, cp_object);

	struct fwstate_map_create_config create = {
		.index_size = 1024,
		.extra_bucket_count = 64,
		.worker_count = 1,
	};
	TEST_ASSERT_SUCCESS(
		fwstate_map_nat64_object_create(obj, &create),
		"nat64state map create failed"
	);

	struct fwstate_map_link link;
	struct fwstate_map_link wan_link;
	fwstate_map_nat64_object_link(cp, 0, &link, &wan_link);
	TEST_ASSERT_NOT_NULL(link.table, "worker 0 must link the lan table");
	TEST_ASSERT_NOT_NULL(
		wan_link.table, "worker 0 must link the wan table too"
	);
	TEST_ASSERT(link.stash.slot != NULL, "worker 0 must link a stash slot");
	fwstate_map_nat64_object_link(cp, 3, &link, &wan_link);
	TEST_ASSERT(
		link.table != NULL && wan_link.table != NULL &&
			link.stash.slot == NULL,
		"a worker outside the stash keeps the tables but no slot"
	);

	struct nat64state_lan_key key = lan_key(IPPROTO_TCP, 5000, 443);

	struct nat64state_value first = {0};
	first.created_at = 100;
	first.updated_at = 100;
	first.last_ttl = 30ull * 1000000000ull;
	first.ip4 = 0x0a000001;
	first.port = 40000;
	first.tcp_established = true;

	rwlock_t *lock = NULL;
	TEST_ASSERT(
		fwtable_insert(
			link.table, 0, 100, first.last_ttl, &key, &first, &lock
		) >= 0,
		"lan session insert failed"
	);
	rwlock_write_unlock(lock);

	// A refresh whose writer raced the first packet and derived a
	// different allocation must not steal the installed one.
	struct nat64state_value refresh = {0};
	refresh.created_at = 200;
	refresh.updated_at = 200;
	refresh.last_ttl = 30ull * 1000000000ull;
	refresh.ip4 = 0x0a000002;
	refresh.port = 40001;
	refresh.tcp_established = false;

	TEST_ASSERT(
		fwtable_insert(
			link.table,
			0,
			200,
			refresh.last_ttl,
			&key,
			&refresh,
			&lock
		) >= 0,
		"lan session refresh failed"
	);
	rwlock_write_unlock(lock);

	void *valuep = NULL;
	bool from_stale = false;
	TEST_ASSERT(
		fwtable_lookup(
			link.table, 210, &key, &valuep, &lock, &from_stale
		) >= 0,
		"lan session lookup failed"
	);
	rwlock_read_unlock(lock);
	struct nat64state_value *seen = valuep;
	TEST_ASSERT_EQUAL(
		seen->ip4, 0x0a000001, "the first allocation must win"
	);
	TEST_ASSERT_EQUAL(seen->port, 40000, "the first port must win");
	TEST_ASSERT_EQUAL(
		seen->created_at, 100, "the creation timestamp must survive"
	);
	TEST_ASSERT_EQUAL(
		seen->updated_at, 200, "the refresh timestamp must apply"
	);
	TEST_ASSERT(
		seen->tcp_established,
		"establishment must latch across the refresh"
	);

	// A key with garbage in its padding bytes is the same key: only the
	// documented fields carry identity.
	struct nat64state_lan_key noisy;
	memset(&noisy, 0xA5, sizeof(noisy));
	noisy.hdr.proto = IPPROTO_TCP;
	noisy.hdr.src_port = 5000;
	noisy.hdr.dst_port = 443;
	memcpy(noisy.src_addr, key.src_addr, 16);
	memcpy(noisy.dst_addr, key.dst_addr, 16);
	TEST_ASSERT(
		fwtable_lookup(
			link.table, 210, &noisy, &valuep, &lock, &from_stale
		) >= 0,
		"a key with garbage padding must find the session"
	);

	// A different key misses.
	struct nat64state_lan_key other = lan_key(IPPROTO_UDP, 5000, 443);
	TEST_ASSERT(
		fwtable_lookup(
			link.table, 210, &other, &valuep, &lock, &from_stale
		) < 0,
		"a different flow must miss"
	);

	fwstate_map_nat64_object_config_free(cp, &err);
	agent_detach(agent);
	return TEST_SUCCESS;
}

// The wan object keys its table by the translated IPv4 tuple, separate
// from the lan family's key space.
static int
test_wan_session(struct yanet_shm *shm) {
	yanet_error *err = NULL;

	struct agent *agent = agent_attach(
		shm, 0, "nat64state-wan", NAT64STATE_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(agent, "agent_attach failed");

	struct cp_object *cp =
		fwstate_map_nat64_object_config_new(agent, "nat0", &err);
	TEST_ASSERT_NOT_NULL(cp, "fwstate_map_nat64_object_config_new failed");

	struct fwstate_map_nat64_object *obj =
		container_of(cp, struct fwstate_map_nat64_object, cp_object);

	struct fwstate_map_create_config create = {
		.index_size = 1024,
		.extra_bucket_count = 64,
		.worker_count = 1,
	};
	TEST_ASSERT_SUCCESS(
		fwstate_map_nat64_object_create(obj, &create),
		"nat64state map create failed"
	);

	struct fwstate_map_link unused_link;
	struct fwstate_map_link link;
	fwstate_map_nat64_object_link(cp, 0, &unused_link, &link);
	TEST_ASSERT_NOT_NULL(link.table, "worker 0 must link the wan table");

	struct nat64state_wan_key key = wan_key(IPPROTO_UDP, 40000, 53);

	struct nat64state_value session = {0};
	session.created_at = 10;
	session.updated_at = 10;
	session.last_ttl = 120ull * 1000000000ull;
	memset(session.src6, 0x20, sizeof(session.src6));

	rwlock_t *lock = NULL;
	TEST_ASSERT(
		fwtable_insert(
			link.table,
			0,
			10,
			session.last_ttl,
			&key,
			&session,
			&lock
		) >= 0,
		"wan session insert failed"
	);
	rwlock_write_unlock(lock);

	void *valuep = NULL;
	bool from_stale = false;
	TEST_ASSERT(
		fwtable_lookup(
			link.table, 20, &key, &valuep, &lock, &from_stale
		) >= 0,
		"wan session lookup failed"
	);
	rwlock_read_unlock(lock);
	struct nat64state_value *seen = valuep;
	TEST_ASSERT_EQUAL(
		seen->src6[0], 0x20, "the wan reverse mapping must be found"
	);

	// Garbage padding on the wan key side is the same key too.
	struct nat64state_wan_key noisy_wan;
	memset(&noisy_wan, 0xA5, sizeof(noisy_wan));
	noisy_wan.hdr.proto = IPPROTO_UDP;
	noisy_wan.hdr.src_port = 40000;
	noisy_wan.hdr.dst_port = 53;
	noisy_wan.local_addr = key.local_addr;
	noisy_wan.remote_addr = key.remote_addr;
	TEST_ASSERT(
		fwtable_lookup(
			link.table, 20, &noisy_wan, &valuep, &lock, &from_stale
		) >= 0,
		"a wan key with garbage padding must find the session"
	);

	// The lan key space and the wan key space are separate tables: the
	// lan key does not hit the wan table.
	struct nat64state_lan_key lan = lan_key(IPPROTO_UDP, 40000, 53);
	TEST_ASSERT(
		fwtable_lookup(
			link.table, 20, &lan, &valuep, &lock, &from_stale
		) < 0,
		"a lan key must not hit the wan table"
	);

	fwstate_map_nat64_object_config_free(cp, &err);
	agent_detach(agent);
	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	const char *port_names[] = {"01:00.0"};
	const char *devs_to_load[] = {"plain"};
	const char *objs_to_load[] = {FWSTATE_MAP_NAT64_OBJECT_TYPE};

	struct dataplane_ut_config cfg = {
		.cp_memory = 1u << 24,
		.dp_memory = 1u << 20,
		.worker_count = 1,
		.devices = port_names,
		.device_count = 1,
		.modules = NULL,
		.module_count = 0,
		.devices_to_load = devs_to_load,
		.devices_to_load_count = 1,
		.objects_to_load = objs_to_load,
		.objects_to_load_count = 1,
	};

	struct dataplane_ut *ut = dataplane_ut_new(&cfg);
	if (ut == NULL) {
		fprintf(stderr, "dataplane_ut_new failed\n");
		return 1;
	}

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	if (shm == NULL) {
		fprintf(stderr, "dataplane_ut_shm returned NULL\n");
		dataplane_ut_free(ut);
		return 1;
	}

	int res = test_lan_session_identity(shm);
	if (res == TEST_SUCCESS) {
		res = test_wan_session(shm);
	}

	dataplane_ut_free(ut);

	return (res == TEST_SUCCESS) ? 0 : 1;
}
