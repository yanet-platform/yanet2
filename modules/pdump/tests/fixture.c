#include "fixture.h"

#include <stdlib.h>
#include <string.h>

#include <bpf_impl.h>
#include <pcap/pcap.h>
#include <rte_bpf.h>
#include <rte_malloc.h>
#include <rte_mbuf.h>

#include "api/agent.h"

#include "common/memory_address.h"

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/dataplane_ut/dataplane_ut.h"

#include "objects/ring/api/ring_object.h"

#define PDUMP_TEST_MEMORY_LIMIT (4u * 1024u * 1024u)

struct rte_bpf *
pdump_test_compile_accept_ip_filter(uint32_t snaplen, yanet_error **err) {
	pcap_t *pcap = pcap_open_dead(DLT_EN10MB, snaplen);
	if (pcap == NULL) {
		yanet_error_add(err, "pcap_open_dead failed");
		return NULL;
	}

	struct bpf_program bf;
	if (pcap_compile(pcap, &bf, "ip", 1, PCAP_NETMASK_UNKNOWN) != 0) {
		yanet_error_add(
			err, "pcap_compile failed: %s", pcap_geterr(pcap)
		);
		pcap_close(pcap);
		return NULL;
	}

	struct rte_bpf_prm *prm = rte_bpf_convert(&bf);
	pcap_freecode(&bf);
	pcap_close(pcap);
	if (prm == NULL) {
		yanet_error_add(err, "rte_bpf_convert failed");
		return NULL;
	}

	struct rte_bpf *loaded = rte_bpf_load(prm);
	rte_free(prm);
	if (loaded == NULL) {
		yanet_error_add(err, "rte_bpf_load failed");
		return NULL;
	}

	uint64_t buf_sz = loaded->sz;
	uint8_t *buf = malloc(buf_sz);
	if (buf == NULL) {
		yanet_error_add(err, "failed to allocate the filter copy");
		rte_bpf_destroy(loaded);
		return NULL;
	}
	memcpy(buf, loaded, buf_sz);
	rte_bpf_destroy(loaded);

	struct rte_bpf *bpf = (struct rte_bpf *)buf;
	// JIT is not wired into the dataplane's own copy either; keep the
	// interpreter path the real handler always runs.
	bpf->jit.func = NULL;
	bpf->jit.sz = 0;

	size_t bsz = sizeof(bpf[0]);
	size_t xsz = (size_t)bpf->prm.nb_xsym * sizeof(struct rte_bpf_xsym);
	SET_OFFSET_OF(&bpf->prm.xsym, (struct rte_bpf_xsym *)(buf + bsz));
	SET_OFFSET_OF(&bpf->prm.ins, (struct ebpf_insn *)(buf + bsz + xsz));

	return bpf;
}

struct packet *
pdump_test_build_packet(
	struct dataplane_ut *ut, bool ipv4, uint16_t total_len
) {
	struct rte_mbuf *mbuf = dataplane_ut_alloc_mbuf(ut);
	if (mbuf == NULL) {
		return NULL;
	}
	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(*packet));
	packet->mbuf = mbuf;

	uint8_t *data = (uint8_t *)rte_pktmbuf_append(mbuf, total_len);
	if (data == NULL) {
		rte_pktmbuf_free(mbuf);
		return NULL;
	}
	for (uint16_t i = 0; i < total_len; ++i) {
		data[i] = (uint8_t)i;
	}
	if (total_len >= 14) {
		data[12] = ipv4 ? 0x08 : 0x99;
		data[13] = ipv4 ? 0x00 : 0x99;
	}
	packet->data_len = packet_data_len(packet);
	return packet;
}

int
pdump_test_gen_init(struct pdump_test_gen *gen, size_t worker_count) {
	memset(gen, 0, sizeof(*gen));
	gen->ectxs = calloc(worker_count, sizeof(gen->ectxs[0]));
	gen->ectx_ptrs = calloc(worker_count, sizeof(gen->ectx_ptrs[0]));
	if (gen->ectxs == NULL || gen->ectx_ptrs == NULL) {
		free(gen->ectxs);
		free(gen->ectx_ptrs);
		gen->ectxs = NULL;
		gen->ectx_ptrs = NULL;
		return TEST_FAILED;
	}

	for (size_t idx = 0; idx < worker_count; ++idx) {
		SET_OFFSET_OF(
			&gen->ectxs[idx].cp_config_gen, &gen->cp_config_gen
		);
		SET_OFFSET_OF(&gen->ectx_ptrs[idx], &gen->ectxs[idx]);
	}
	SET_OFFSET_OF(&gen->cp_config_gen.config_gen_ectxs, gen->ectx_ptrs);
	gen->cp_config_gen.config_gen_ectx_count = worker_count;
	return TEST_SUCCESS;
}

void
pdump_test_gen_destroy(struct pdump_test_gen *gen) {
	free(gen->ectxs);
	free(gen->ectx_ptrs);
}

struct config_gen_ectx *
pdump_test_gen_worker_ectx(struct pdump_test_gen *gen, uint64_t worker_idx) {
	if (worker_idx >= gen->cp_config_gen.config_gen_ectx_count) {
		return NULL;
	}
	return &gen->ectxs[worker_idx];
}

int
pdump_test_fixture_build(
	struct dataplane_ut *ut,
	const struct pdump_test_fixture_params *params,
	struct pdump_test_fixture *fx
) {
	memset(fx, 0, sizeof(*fx));
	fx->module = params->module;
	fx->gen = params->gen;

	yanet_error *err = NULL;

	struct yanet_shm *shm = dataplane_ut_shm(ut);
	TEST_ASSERT_NOT_NULL(shm, "dataplane_ut_shm returned NULL");

	fx->agent = agent_attach(
		shm, 0, params->agent_name, PDUMP_TEST_MEMORY_LIMIT, &err
	);
	TEST_ASSERT_NOT_NULL(
		fx->agent,
		"agent_attach failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	fx->ring_object = ring_object_config_new(
		fx->agent,
		params->ring_name,
		params->capacity,
		params->publish_batch,
		&err
	);
	TEST_ASSERT_NOT_NULL(
		fx->ring_object,
		"ring_object_config_new failed: %s",
		err ? yanet_error_message(err) : "?"
	);

	fx->ring = ring_object_worker(fx->ring_object, params->worker_idx);
	fx->ring_data =
		ring_object_worker_data(fx->ring_object, params->worker_idx);
	TEST_ASSERT_NOT_NULL(
		fx->ring,
		"ring object has no worker %lu",
		(unsigned long)params->worker_idx
	);
	TEST_ASSERT_NOT_NULL(
		fx->ring_data,
		"ring object worker %lu has no data area",
		(unsigned long)params->worker_idx
	);

	fx->config.snaplen = params->snaplen;
	fx->config.mode = PDUMP_INPUT;
	fx->config.ring_link_idx = 0;
	SET_OFFSET_OF(&fx->config.ebpf_program, params->bpf);

	fx->ring_object_ectx.abs_cp_object = fx->ring_object;
	fx->link.abs_object_ectx = &fx->ring_object_ectx;
	fx->module_ectx.object_link_count = 1;
	fx->module_ectx.abs_object_links = &fx->link;
	fx->module_ectx.abs_cp_module = &fx->config.cp_module;

	fx->dp_worker.idx = params->worker_idx;
	fx->dp_worker.current_time = PDUMP_TEST_TIMESTAMP;

	fx->prepared = calloc(1, fx->module->prepared_size);
	TEST_ASSERT_NOT_NULL(fx->prepared, "failed to allocate prepared");
	SET_OFFSET_OF(&fx->module_ectx.module_prepared, fx->prepared);
	fx->module_ectx.abs_module_prepared = fx->prepared;

	pdump_test_fixture_commit(fx);

	return TEST_SUCCESS;
}

void
pdump_test_fixture_commit(struct pdump_test_fixture *fx) {
	fx->module_ectx.abs_config_gen_ectx =
		pdump_test_gen_worker_ectx(fx->gen, fx->dp_worker.idx);
	fx->module->commit_ectx_handler(
		&fx->module_ectx, &fx->config.cp_module
	);
}

void
pdump_test_fixture_destroy(struct pdump_test_fixture *fx) {
	free(fx->prepared);
	if (fx->ring_object != NULL) {
		yanet_error *err = NULL;
		ring_object_config_free(fx->ring_object, &err);
		yanet_error_free(err);
	}
	if (fx->agent != NULL) {
		agent_detach(fx->agent);
	}
}

void
pdump_test_free_front(struct packet_front *pf) {
	struct packet_list *lists[] = {&pf->input, &pf->output, &pf->drop};
	for (size_t idx = 0; idx < sizeof(lists) / sizeof(lists[0]); ++idx) {
		struct packet *packet;
		while ((packet = packet_list_pop(lists[idx])) != NULL) {
			rte_pktmbuf_free(packet_to_mbuf(packet));
		}
	}
}
