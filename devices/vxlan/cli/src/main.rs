use core::net::Ipv4Addr;
use std::borrow::Cow;

use clap::{CommandFactory, Parser, value_parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{Device, DevicePipeline, IPv4Address, MacAddress};
use netip::MacAddr;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use vxlanpb::{
    ShowDeviceVxlanRequest, UpdateDeviceVxlanRequest, VxlanTunnel,
    device_vxlan_service_client::DeviceVxlanServiceClient, device_vxlan_service_server::SERVICE_NAME,
};
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion,
    display::{self, KeyValue},
    errors::Error,
    output,
};
use ynpb::pb::{ListDevicesRequest, device_service_client::DeviceServiceClient};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod vxlanpb {
    tonic::include_proto!("devices.vxlan.controlplane.vxlanpb.v1");
}

/// Manages vxlan devices.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Show the pipeline bindings and the tunnel of a device.
    Show(ShowCmd),
    /// Create or replace a device.
    Update(UpdateCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Show(..) => "show",
            Self::Update(..) => "update",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowCmd {
    /// Name of the device to show.
    #[arg(add = ArgValueCandidates::new(device_candidates))]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the device.
    #[arg(long, short = 'n')]
    pub name: String,
    /// Pipeline assignments in format "pipeline_name:weight".
    #[arg(long, short = 'i')]
    pub input: Vec<DevicePipeline>,
    /// Pipeline assignments in format "pipeline_name:weight".
    #[arg(long, short = 'o')]
    pub output: Vec<DevicePipeline>,
    /// Local tunnel endpoint: the outer source address and the only destination
    /// decapsulated.
    #[arg(long)]
    pub local_ip: Ipv4Addr,
    /// Remote tunnel endpoint: the outer destination address.
    #[arg(long)]
    pub remote_ip: Ipv4Addr,
    /// Outer source hardware address.
    #[arg(long)]
    pub local_mac: MacAddr,
    /// Outer destination hardware address, the underlay next hop.
    #[arg(long)]
    pub remote_mac: MacAddr,
    /// VXLAN network identifier in 0..=16777215.
    #[arg(long, value_parser = value_parser!(u32).range(0..=16_777_215))]
    pub vni: u32,
}

type DeviceVxlanService = Service<DeviceVxlanServiceClient<LayeredChannel>>;

async fn show_device(service: &mut DeviceVxlanService, cmd: ShowCmd) -> Result<(), Error> {
    let name = cmd.name;
    let request = ShowDeviceVxlanRequest { name: name.clone() };

    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("vxlan device '{name}'")),
            async |client, request| client.show_device(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            let rows = response.device.as_ref().map(binding_rows).unwrap_or_default();

            if let Some(tunnel) = response.tunnel.as_ref() {
                tunnel_block(tunnel).print();
            }

            if rows.is_empty() {
                output::empty_with_hint(
                    format_args!("No pipeline bindings found for '{name}'."),
                    format_args!(
                        "bind one with 'yanet-cli device vxlan update -n <name> -i <pipeline:weight> -o <pipeline:weight> --local-ip <ip> --remote-ip <ip> --local-mac <mac> --remote-mac <mac> --vni <id>'"
                    ),
                );
                return;
            }

            display::print_table_from_entries(rows);
        },
    );

    Ok(())
}

async fn update_config(service: &mut DeviceVxlanService, cmd: UpdateCmd) -> Result<(), Error> {
    let request = UpdateDeviceVxlanRequest {
        name: cmd.name.clone(),
        device: Some(Device { input: cmd.input, output: cmd.output }),
        tunnel: Some(VxlanTunnel {
            local_ip: Some(IPv4Address::from(cmd.local_ip)),
            remote_ip: Some(IPv4Address::from(cmd.remote_ip)),
            local_mac: Some(MacAddress::from(cmd.local_mac)),
            remote_mac: Some(MacAddress::from(cmd.remote_mac)),
            vni: cmd.vni,
        }),
    };

    service
        .unary("update", request, async |client, request| {
            client.update_device(request).await
        })
        .await?;

    output::success("update", format_args!("Updated device '{}'.", cmd.name));

    Ok(())
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, |channel| {
        DeviceVxlanServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => show_device(&mut service, cmd).await,
        ModeCmd::Update(cmd) => update_config(&mut service, cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Renders the tunnel; an endpoint the response omits is skipped, not shown
/// blank.
fn tunnel_block(tunnel: &VxlanTunnel) -> KeyValue {
    let mut block = KeyValue::new();
    if let Some(ip) = tunnel.local_ip.as_ref() {
        block = block.row("Local IP", ip);
    }
    if let Some(ip) = tunnel.remote_ip.as_ref() {
        block = block.row("Remote IP", ip);
    }
    if let Some(mac) = tunnel.local_mac.as_ref() {
        block = block.row("Local MAC", mac);
    }
    if let Some(mac) = tunnel.remote_mac.as_ref() {
        block = block.row("Remote MAC", mac);
    }

    block.row("VNI", tunnel.vni)
}

/// One row of a device's pipeline bindings table.
struct BindingRow {
    direction: &'static str,
    pipeline: String,
    weight: u64,
}

impl Tabled for BindingRow {
    const LENGTH: usize = 3;

    fn fields(&self) -> Vec<Cow<'_, str>> {
        vec![
            Cow::Borrowed(self.direction),
            Cow::Borrowed(self.pipeline.as_str()),
            Cow::Owned(self.weight.to_string()),
        ]
    }

    fn headers() -> Vec<Cow<'static, str>> {
        vec![
            Cow::Borrowed("DIRECTION"),
            Cow::Borrowed("PIPELINE"),
            Cow::Borrowed("WEIGHT"),
        ]
    }
}

/// Flattens a device's input and output pipeline bindings into rows sorted
/// by direction, then pipeline name.
fn binding_rows(device: &Device) -> Vec<BindingRow> {
    let mut rows: Vec<BindingRow> = device
        .input
        .iter()
        .map(|binding| BindingRow {
            direction: "input",
            pipeline: binding.name.clone(),
            weight: binding.weight,
        })
        .chain(device.output.iter().map(|binding| BindingRow {
            direction: "output",
            pipeline: binding.name.clone(),
            weight: binding.weight,
        }))
        .collect();

    rows.sort_by(|a, b| a.direction.cmp(b.direction).then_with(|| a.pipeline.cmp(&b.pipeline)));

    rows
}

fn device_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, device_client, async move |mut client| {
        Ok(client
            .list(ListDevicesRequest {})
            .await?
            .into_inner()
            .ids
            .into_iter()
            .filter(|id| id.r#type == "vxlan")
            .map(|id| id.name)
            .collect())
    })
}

fn device_client(channel: LayeredChannel) -> DeviceServiceClient<LayeredChannel> {
    DeviceServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_binding_rows_sorts_by_direction_then_pipeline() {
        let device = Device {
            input: vec![
                DevicePipeline { name: "b".to_string(), weight: 2 },
                DevicePipeline { name: "a".to_string(), weight: 1 },
            ],
            output: vec![DevicePipeline { name: "a".to_string(), weight: 3 }],
        };

        let rows = binding_rows(&device);
        let got: Vec<(&str, &str, u64)> = rows
            .iter()
            .map(|row| (row.direction, row.pipeline.as_str(), row.weight))
            .collect();

        assert_eq!(vec![("input", "a", 1), ("input", "b", 2), ("output", "a", 3)], got);
    }

    #[test]
    fn test_tunnel_block_renders_endpoints_and_vni() {
        let tunnel = VxlanTunnel {
            local_ip: Some(IPv4Address::from(Ipv4Addr::new(192, 0, 2, 1))),
            remote_ip: Some(IPv4Address::from(Ipv4Addr::new(198, 51, 100, 2))),
            local_mac: Some(MacAddress::from(MacAddr::from([0x02, 0, 0, 0, 0, 0x01]))),
            remote_mac: Some(MacAddress::from(MacAddr::from([0x02, 0, 0, 0, 0, 0x02]))),
            vni: 100,
        };

        let block = tunnel_block(&tunnel);
        let got: Vec<(&str, &str)> = block
            .entries()
            .iter()
            .map(|(key, values)| (key.as_str(), values[0].as_str()))
            .collect();

        assert_eq!(
            vec![
                ("Local IP", "192.0.2.1"),
                ("Remote IP", "198.51.100.2"),
                ("Local MAC", "02:00:00:00:00:01"),
                ("Remote MAC", "02:00:00:00:00:02"),
                ("VNI", "100"),
            ],
            got
        );
    }
}
