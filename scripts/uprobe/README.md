# yanet-uprobe

`yanet-uprobe` attaches userspace probes to a running YANET2 process without
a restart. Install `yanet2-uprobe` and the `yanet2-controlplane-dbgsym`
matching the running CP on Ubuntu 24.04 amd64 or arm64. The dependency is
unversioned: `setup` matches debug symbols to the target executable by
build-id, so yanet-uprobe can be upgraded independently of CP. The package
includes [bpftime v0.9.0](https://github.com/eunomia-bpf/bpftime/tree/v0.9.0).

## cp-lock

The `cp-lock` probe measures acquisitions, failed try-locks, wait/hold times and
hold histograms per caller in one CP process.
Prometheus `site` is `function@file:line`; CP source paths are repository-relative
when the debug data identifies the root. External paths are preserved.
Calls at the same source location are combined. Unavailable source locations
appear as `??:?` and are combined within each function.

```sh
sudo yanet-uprobe cp-lock setup --pid "$CP_PID"
sudo yanet-uprobe cp-lock report                    # sorted by total hold time
sudo yanet-uprobe cp-lock report --format prometheus
sudo yanet-uprobe cp-lock stop                      # clear counters, keep CP alive
```

The table shows average and maximum durations for completed lock/unlock pairs.
`TOTAL MAX` is the largest wait + hold time observed in a single call.
`HOLD P95~` estimates the 95th percentile using rounded log2 bucket upper edges.
Statistics cover the whole session; rows are sorted by total hold time.

Setup exits while profiling continues. Repeating setup preserves counters and
accepts a new matching `--debug-file`; add `--replace` to reset counters with a
collection gap. Stop before upgrading or
uninstalling; the runtime stays mapped until CP exits. Attach requires ptrace
permission, the original executable inode and matching debug symbols; executable
paths cannot contain whitespace.

Sessions created before `TOTAL MAX` require `setup --pid "$CP_PID" --replace`
to update probes and reset counters without restarting CP.

For collection through existing Telegraf outputs:

```toml
[[inputs.exec]]
  commands = ["/usr/bin/yanet-uprobe cp-lock report --format prometheus"]
  interval = "10s"
  timeout = "5s"
  data_format = "prometheus"
  prometheus_metric_version = 2
```

The monitoring UID needs read access to `/dev/shm/bpftime_maps_shm`, its
`.uprobe` record, `/proc/PID/maps` and debug files. Reports are cumulative;
concurrent fields may reflect different moments and converge after writes finish.
Use `cp_lock_session_info` and `cp_lock_sample_time_seconds` to detect resets and
stale samples.
`report --push URL` also posts one Prometheus sample to an HTTP endpoint.

Local build: `cd scripts/uprobe && ./prepare.sh && make`.
Requires Go 1.27.1, clang with BPF support and libbpf development files.
`prepare.sh` downloads a pinned runtime archive from a yanet2 GitHub release
and verifies its hashes; the package build never compiles bpftime. Both
archives come from the manual workflow mode described below. For in-tree builds, placing an archive
with the pinned hash at `.deps/bpftime-<arch>.tar.gz` skips the download.
`make selftest` checks real locks and session lifecycle using the project's
configured DPDK build. Installed hosts need no build tools.

Runtime archives are built from pinned upstream v0.9.0 on a native GitHub
Actions VM by a manual workflow mode that runs `make selftest` and publishes the
archive with checksums as a prerelease; ordinary CI never builds bpftime:
`gh workflow run build-deb.yml -f build_bpftime=amd64` (or `arm64`).
Pin the published archive URL, archive hash, runtime hashes and `BUILD`
identity in `prepare.sh` after the run.
