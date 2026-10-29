# Field notes: what real YANET deployments look like

Research snapshot (October 2026) from public articles, talks and repo
discussions. One confirmed production operator - Yandex; that is not
snobbery of sources but a verified negative: forks, issues, conference
programs and job postings surface no other company publicly running it.
Everything below is first-party Yandex material unless marked inference.

## Who runs it and why

**Yandex** runs YaNet (their spelling) as the company-wide
"traffic-moving" platform: hundreds of commodity servers across 10+ DCs and
IX points of presence, each box up to 200 Gbps [839508]. Three role
families, which is exactly why the recipes in use-cases.md are ranked the
way they are:

1. **L3/L4 load balancing** - the external/internal balancing fleet:
   ~400 balancers, ~12 000 virtual services, 2x100G per box, ~1.4 Tbps
   measured total. Replaced IPVS + iptables Linux boxes (capped ~10M
   sessions / 25-30 Gbit/s; rulesets stopped fitting a 512 MB netlink
   message). DPVS, VPP and Katran were evaluated and rejected (missing
   features, integration cost). All Yandex DNS balancing runs on it since
   Q1 2023 [967212].
2. **Stateful firewalls** - IPFW-style rulesets at 100 Gbps+ with constant
   lookup depth [967212].
3. **Tunneling / DC border** - datacenters are IPv6-only internally, so the
   edge runs NAT64, tun64 and IPIP decapsulation on YANET boxes [839508].

The full Internet-facing BGP border was still on vendor chassis routers as
of the Feb 2023 outage postmortem [716524] - treat "replaces the peering
router" as a maybe, "DC edge / balancer / firewall" as proven (inference
from both sources).

## Numbers worth quoting in capacity talks

| Figure | Source |
|---|---|
| up to 200 Gbps per server, hundreds of servers, 10+ DCs/PoPs | 839508 |
| ~1.4 Tbps total balanced traffic; ~90 % of ~1.5 Tbps east-west internal | 967212 |
| ~400 balancers, 12 000 virtual services, up to ~100k health-checked endpoints | 967212 |
| firewall "easily 100 Gbit/s"; ruleset compile 40-90 s | 967212 |
| 40M DNS requests in a load test (bang generator) | 844298 |
| config delivery p50 < 4 min, 20-30 configs / 4 min per box, 99 % robot clients | 967212 |

## Operational lessons (translate directly into host prep)

- Commodity servers, 3-4 NICs (two for traffic, rest management); roles not
  mixed - a balancer is not also a tunneler [839508].
- Mellanox ConnectX-4/5/6 are the production-tested NICs [yanet#39].
- BIOS: turbo boost and hyperthreading off; cores strictly pinned; plan for
  YANET to "use almost the whole machine" and co-locate agents accordingly
  [839508]. GB-scale memory for firewall rulesets is normal [967212].
- Config delivery moved from SSH push to pull-based agents with a state
  machine; k8s orchestration of YANET services presented at infra.conf 2024
  [821859] - the same direction yanet2's operator layer takes.
- DDoS: a stateful L4 balancer is the weak link under SYN floods; Yandex's
  answer is nearly-stateless double consistent hashing
  (github.com/kndrvt/mhs), not bigger tables [858662].
- Per-host independence was cited as accidental HA strength; customer
  guarantee: no degradation on loss of one DC or half the balancing capacity
  of any DC [839508, 967212].
- Monolith pain that motivated the yanet2 rewrite: slow config reloads,
  weak balancer statistics [967212] - i.e. the operator/reconcile layer this
  repo centers on exists to fix exactly that.

## Sources

- habr.com/ru/articles/839508/ - YaNet operations on bare metal (Aug 2024)
- habr.com/ru/articles/967212/ - load-balancing history and YANET migration (Nov 2025)
- habr.com/ru/articles/858662/ - nearly-stateless L4 balancing + github.com/kndrvt/mhs
- habr.com/ru/articles/844298/ - bang DNS load generator
- habr.com/ru/articles/821859/ - YANET services on k8s (infra.conf 2024)
- habr.com/ru/articles/716524/ - Feb 2023 outage postmortem (vendor border)
- github.com/yanet-platform/yanet (upstream, Apache-2.0) and
  github.com/yanet-platform/yanet2 (this repo's upstream)
- github.com/yanet-platform/yanet/issues/39 - NICs, KNI, DPVS/VPP comparison
- nexthopconf.com/2023 - Yandex network conference; YANET talk + workshop
