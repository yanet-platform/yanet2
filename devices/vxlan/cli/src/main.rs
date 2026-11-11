use core::net::Ipv4Addr;
use std::borrow::Cow;

use clap::{CommandFactory, Parser, value_parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{Device, DevicePipeline};
use netip::MacAddr;
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use vxlanpb::{
    GetDeviceVxlanRequest, UpdateDeviceVxlanRequest, device_vxlan_service_client::DeviceVxlanServiceClient,
    device_vxlan_service_server::SERVICE_NAME,
};
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};
use ynpb::pb::{ListDevicesRequest, device_service_client::DeviceServiceClient};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod vxlanpb {
    tonic::include_proto!("devices.vxlan.controlplane.vxlanpb.v1");
}

/// Manages vxlan tunnel devices.
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
    /// Show the tunnel parameters and pipeline bindings of a device.
    Get(GetCmd),
    /// Create or replace a device.
    Update(UpdateCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Get(..) => "get",
            Self::Update(..) => "update",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct GetCmd {
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
    /// VXLAN network identifier in 0..=16777215.
    #[arg(long, value_parser = value_parser!(u32).range(0..=0xFFFFFF))]
    pub vni: u32,
    /// Outer UDP destination port in 1..=65535.
    #[arg(long, value_parser = value_parser!(u16).range(1..=65535))]
    pub dst_port: u16,
    /// Outer source MAC address in aa:bb:cc:dd:ee:ff notation.
    #[arg(long)]
    pub src_mac: MacAddr,
    /// Outer destination MAC address in aa:bb:cc:dd:ee:ff notation.
    #[arg(long)]
    pub dst_mac: MacAddr,
    /// Outer source IPv4 underlay address.
    #[arg(long)]
    pub src_ip: Ipv4Addr,
    /// Outer destination IPv4 underlay address.
    #[arg(long)]
    pub dst_ip: Ipv4Addr,
}

type DeviceVxlanService = Service<DeviceVxlanServiceClient<LayeredChannel>>;

async fn get_device(service: &mut DeviceVxlanService, cmd: GetCmd) -> Result<(), Error> {
    let name = cmd.name;
    let request = GetDeviceVxlanRequest { name: name.clone() };

    let response = service
        .unary_with(
            "get",
            request,
            service.not_found("get", &format!("vxlan device '{name}'")),
            async |client, request| client.get_device(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            display::KeyValue::new()
                .row("vni", response.vni)
                .row("dst port", response.dst_port)
                .row("src mac", &response.src_mac)
                .row("dst mac", &response.dst_mac)
                .row("src ip", &response.src_ip)
                .row("dst ip", &response.dst_ip)
                .print();

            let rows = response.device.as_ref().map(binding_rows).unwrap_or_default();

            if rows.is_empty() {
                output::empty_with_hint(
                    format_args!("No pipeline bindings found for '{name}'."),
                    format_args!(
                        "bind one with 'yanet-cli device vxlan update -n <name> -i <pipeline:weight> -o <pipeline:weight> --vni <id>'"
                    ),
                );
                return;
            }

            display::print_table_from_entries(rows);
        },
    );

    Ok(())
}

async fn update_device(service: &mut DeviceVxlanService, cmd: UpdateCmd) -> Result<(), Error> {
    let request = UpdateDeviceVxlanRequest {
        name: cmd.name.clone(),
        device: Some(Device { input: cmd.input, output: cmd.output }),
        vni: cmd.vni,
        dst_port: u32::from(cmd.dst_port),
        src_mac: cmd.src_mac.to_string(),
        dst_mac: cmd.dst_mac.to_string(),
        src_ip: cmd.src_ip.to_string(),
        dst_ip: cmd.dst_ip.to_string(),
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
            .send_compressed(CompressionEncoding::Zstd)
            .accept_compressed(CompressionEncoding::Zstd)
            .accept_compressed(CompressionEncoding::Gzip)
    })
    .await?;

    match cmd.mode {
        ModeCmd::Get(cmd) => get_device(&mut service, cmd).await,
        ModeCmd::Update(cmd) => update_device(&mut service, cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
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
        .send_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Zstd)
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
}
