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
