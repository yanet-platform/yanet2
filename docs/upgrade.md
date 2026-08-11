# Upgrade notes

Manual steps required when upgrading an existing deployment. The packaging
does not perform these automatically.

## Controlplane systemd unit became a template

`yanet2-controlplane.service` is replaced by the template
`yanet2-controlplane@.service`. The instance name selects the config:
`yanet2-controlplane@<name>` runs `/etc/yanet2/controlplane.d/<name>.yaml`.

Upgrading does not stop or disable the old unit, so its process keeps holding
`[::1]:8080` and `/dev/hugepages/yanet`. Enabling an instance before removing
it gives you two control planes fighting over both. On each host:

1. Retire the old unit:

   ```bash
   systemctl disable --now yanet2-controlplane.service
   systemctl daemon-reload
   ```

2. Move the site config into place. The old unit read
   `/etc/yanet2/controlplane.yaml`, which nothing reads any more:

   ```bash
   mv /etc/yanet2/controlplane.yaml /etc/yanet2/controlplane.d/default.yaml
   ```

   The packaged example moves itself from
   `/etc/yanet2/controlplane-default.yaml` to
   `/etc/yanet2/controlplane.d/default.yaml` during the upgrade, so it is
   already there if you never wrote a site config. It logs at `debug` and
   binds `[::1]:8080` — review it before running it in production.

3. Enable the instance. No instance is enabled by the package:

   ```bash
   systemctl enable --now yanet2-controlplane@default
   ```

Instances are grouped under `yanet2-controlplane.target`, which the operators
order against, so `systemctl restart yanet2-controlplane.target` reaches every
running instance.

Running more than one instance needs per-instance `endpoint`, `http_endpoint`
and `memory_path` values in each config, and the operators point at
`[::1]:8080` in their own configs.

## `instance_id` is now mandatory on the gateway and on every module and device

The gateway and every module or device block in the controlplane config now require an explicit `instance_id`, naming which dataplane instance that entry serves. `0` remains a perfectly valid value, it just has to be written down. A config that omits `instance_id` anywhere it applies fails to load, and the director refuses to start.

This is a dpkg conffile, so an upgrade preserves your site-modified copy of `/etc/yanet2/controlplane.d/<name>.yaml` unchanged. Since every config written before this change predates `instance_id`, the director will refuse to start right after the package upgrade until you edit it in.

The same change also makes the module and device list exhaustive: a module or device left out of the config is no longer started at all, and no longer attaches a shared-memory agent. Before this change every bundled module started regardless of whether the config mentioned it, defaulting to instance 0 — which is the production defect this fixes. Add an explicit block for every module and device you actually rely on; do not assume the old implicit set still starts.

1. Add `instance_id` to the `gateway:` block and to every `modules:`/`devices:` entry in your site config:

   ```yaml
   gateway:
     instance_id: 0
     server:
       endpoint: "[::1]:8080"
     ...
   modules:
     route:
       instance_id: 0
       ...
   devices:
     plain:
       instance_id: 0
       ...
   ```

2. Add a block for every module and device your deployment actually uses, even if it previously relied on the implicit default set. Use the packaged `/etc/yanet2/controlplane.d/default.yaml` as the worked example of the new shape — it lists every bundled module and the `plain`/`vlan` devices with `instance_id: 0`, but omits `trafgen`. If your deployment uses the trafgen device, add its `instance_id`-carrying block yourself; the packaged example does not do it for you.
