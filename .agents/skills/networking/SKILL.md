---
name: networking
description: >-
  Study the governing RFCs and protocol behaviour for a YANET2 packet path and
  check the implementation against them: NAT64, MPLS, decapsulation, ACL and
  connection tracking, load balancing, routing, DSCP, checksums, MTU/PTB, TTL,
  plus DPDK API fit. Read-only analysis with RFC section citations. Use when a
  change or question touches protocol behaviour or an RFC must be studied.
context: fork
agent: general-purpose
---

# networking

Answer a protocol question for YANET2 from the RFC text and the current code,
not from memory. Read-only: never edit files. In Codex, run this skill in a
fresh subagent and return only the analysis.

## Method

1. Identify the governing RFCs and fetch their text (`https://www.rfc-editor.org/rfc/rfcNNNN.txt`); check errata (`https://www.rfc-editor.org/errata/rfcNNNN`) and whether a later RFC updates or obsoletes it.
2. Quote the exact section numbers and MUST/SHOULD/MAY wording the question turns on.
3. Read the current implementation before judging it; cite `file:line`.
4. Say when an RFC detail is ambiguous or you could not confirm it.

## Domain

NAT64 (RFC 6146/6052/7915), MPLS (RFC 3031/3032/5332), stateful ACL and connection tracking, load balancing (Maglev, ECMP, DSR, IPIP/GRE), routing (LPM, ECMP, next-hop resolution), decapsulation (RFC 2003/2784/7348), DSCP/DiffServ (RFC 2474/2597/3246); DPDK `rte_mbuf`, `rte_lpm`, `rte_hash`, RSS/RETA, PMD offloads, hugepages/NUMA/mempool caching, run-to-completion workers with RCU config swaps; parsing order L2→VLAN→L3→L4, checksums (incremental for NAT), MTU/PTB signalling, TTL handling.

YANET specifics: modules receive a `packet_front` and emit through `packet_front_output`/`packet_front_drop`; config is a pointer swap in shared memory; `fwstate` uses `common/ttlmap/` 5-tuple tables coupled with `acl`.

## Answer (≤ 40 lines, skip empty dimensions)

1. Protocol correctness — RFC section citations, MUST/SHOULD, missed edge cases. 2. Performance — the right DPDK API, batch vs per-packet, cache/NUMA/branch notes; be concrete (`rte_hash_lookup_bulk()` over 16+ packets, not "consider batching"). 3. Design — data-structure fitness, footprint, update cost. 4. Security — RFC-violation risks, flooding/exhaustion/amplification. 5. YANET integration.
