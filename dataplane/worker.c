/*
 * The file is under construction.
 * The first tought was  about provinding device and queue identifiers into
 * worker. Worker processes infinite loop consisting of:
 *  - queue read
 *  - pipepline invocation
 *  - device routing and queue write
 * However the concept is not good as may be because of:
 *  - worker should know about device mapping
 *  - worker should implement logic for logical device routing as vlan/vxlan/
 *    bond/lagg/etc
 *  - multiple pipelines are hard to implement here as the are configured
 *    inside dataplane and shared across workers
 *  - inter-worker-communication to pass packets between numa-nodes
 *
 * New idea is:
 *  - dataplane is responsible for RX/TX and provides function callback to
 *    workers
 *  - concept of logical devices and multiple pipelines, each pipeline could
 *    be assigned to multiple logical devices whereas one logical device
 *    may have only one assigned pipeline. This will reduce pipeline
 *    configuration in case of virtual routers.
 *  - dataplane is reposnsible for L2 processing and routing packets between
 *    logical devices, merging and balancing laggs and so on
 *  - read and writ callbacks should return packages with information about
 *    pipeline assigned to each packet.
 *
 * The only question is how pipeline should be attached to packet/mbuf
 * readings:
 *  - inside packet metadata
 *  - packets are clustered by pipeline and separate array with pipeline range
 *    provided
 *  - anything else
 */

#include "worker.h"

#include "rte_ethdev.h"

#include "pipeline.h"

/*
 * This routine is artifact of previous worker model.
 */
static void
worker_read(struct worker *worker, struct pipeline_front *pipeline_front)
{
	// Allocate on-stack array and read mbufs into it
	struct rte_mbuf **mbufs =
		(struct rte_mbuf **)alloca(sizeof(struct rte_mbuf *) * worker->read_size);
	if (mbufs == NULL) {
		//TODO: log error
		return;
	}

	uint16_t rxSize = worker->read_func(
		worker->read_data,
	        mbufs,
	        worker->read_size);

	for (uint32_t rxIdx = 0; rxIdx < rxSize; ++rxIdx) {
		// Initialize packet metadata and put it into pipeline front
		struct packet *packet = mbuf_to_packet(mbufs[rxIdx]);
		memset(packet, 0, sizeof(struct packet));
		packet->mbuf = mbufs[rxIdx];
		if (parse_packet(packet)) {
			//TODO: should the packet be freed right now?
			pipeline_front_drop(pipeline_front, packet);
			continue;
		}

		pipeline_front_output(pipeline_front, packet);
	}

	return;
}

/*
 * This routine is artifact of previous worker model.
 */
static void
worker_write(struct worker *worker, struct pipeline_front *pipeline_front)
{
	/*
	 * Allocate on-stack array to put packet into it before
	 * submitting to a device
	 */
	struct rte_mbuf **mbufs =
		(struct rte_mbuf **)alloca(sizeof(struct rte_mbuf *) * worker->write_size);
	if (mbufs == NULL) {
		//TODO: log error
		for (struct packet *packet = packet_list_first(&pipeline_front->input);
		     packet != NULL;
		     packet = packet->next) {
			pipeline_front_drop(pipeline_front, packet);
		}

		return;
	}

	uint16_t txSize = 0;
	for (struct packet *packet = packet_list_first(&pipeline_front->input);
	     packet != NULL;
	     packet = packet->next) {

		mbufs[txSize++] = packet_to_mbuf(packet);
		if (txSize < worker->write_size &&
		    packet->next != NULL)
			continue;

		// On stack array is full or last packet is ready
		uint16_t sent = worker->write_func(
			worker->write_data,
		        mbufs,
			txSize);

		for (uint16_t dropIdx = sent; dropIdx < txSize; ++dropIdx) {
			// Move packet in drop list in case of TX error
			pipeline_front_drop(pipeline_front, mbuf_to_packet(mbufs[dropIdx]));
		}
		txSize = 0;
	}
}

static void
worker_drop(struct worker *worker, struct pipeline_front *pipeline_front)
{
	(void) worker;

	for (struct packet *packet = packet_list_first(&pipeline_front->drop);
	     packet != NULL;
	     packet = packet->next) {
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		rte_pktmbuf_free(mbuf);
	}
}

static void
worker_loop(struct worker *worker, struct pipeline *pipeline)
{
	/*
	 * The worker loop is simple and consists of following stages:
	 * - read
	 * - process
	 * - write
	 * - drop
	 */
	while (!worker->stop) {
		struct pipeline_front pipeline_front;
		pipeline_front_init(&pipeline_front);

		worker_read(worker, &pipeline_front);
		pipeline_process(pipeline, &pipeline_front);
		worker_write(worker, &pipeline_front);
		worker_drop(worker, &pipeline_front);
	}
}

void
worker_exec(
	struct pipeline *pipeline,
	worker_read_func read_func, void *read_data,
	worker_write_func write_func, void *write_data)
{
	struct worker worker;

	worker.read_size = 16;
	worker.read_func = read_func;
	worker.read_data = read_data;

	worker.write_size = 16;
	worker.write_func = write_func;
	worker.write_data = write_data;

	worker_loop(&worker, pipeline);
}
