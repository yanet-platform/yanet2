# Control-plane lock profiling

Install `yanet2-cp-lock-bpftime` and its exact-version dependency
`yanet2-controlplane-dbgsym` on Ubuntu 24.04 amd64. The package includes
[bpftime v0.9.0](https://github.com/eunomia-bpf/bpftime/tree/v0.9.0).
Userspace probes measure acquisitions, failed try-locks, wait/hold times and
hold histograms per caller in one CP process.
Prometheus `site` is `function@file:line`; calls at the same source location
are combined. Unavailable source locations appear as `??:?` and are combined
within each function.

```sh
sudo yanet-cp-lock setup --pid "$CP_PID"
sudo yanet-cp-lock report                    # sorted by total hold time
sudo yanet-cp-lock report --format prometheus
sudo yanet-cp-lock stop                      # clear counters, keep CP alive
```

Setup exits while profiling continues. Repeating setup preserves counters;
add `--replace` to reset them with a collection gap. Stop before upgrading or
uninstalling; the runtime stays mapped until CP exits. Attach requires ptrace
permission, the original executable inode and matching debug symbols; executable
paths cannot contain whitespace. To profile from process startup instead:

```sh
sudo yanet-cp-lock setup --launch -- /usr/bin/yanet-controlplane ARGUMENTS
```

For collection through existing Telegraf outputs:

```toml
[[inputs.exec]]
  commands = ["/usr/bin/yanet-cp-lock report --format prometheus"]
  interval = "10s"
  timeout = "5s"
  data_format = "prometheus"
  prometheus_metric_version = 2
```

The monitoring UID needs read access to `/dev/shm/bpftime_maps_shm`, its
`.cp-lock` record, `/proc/PID/maps` and debug files. Reports are cumulative;
use `cp_lock_session_info` and `cp_lock_sample_time_seconds` to detect resets
and stale samples.
`report --push URL` also posts one Prometheus sample to an HTTP endpoint.

Local build: `cd scripts/bpftime/cp-lock && ./prepare.sh && make`.
Requires Go 1.27.1, clang with BPF support and libbpf development files.
Docker is needed only to extract the pinned prebuilt runtime during preparation.
`make selftest` checks real locks and session lifecycle using the project's
configured DPDK build. Installed hosts need no Docker or build tools.
