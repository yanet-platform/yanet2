use std::borrow::Cow;

use clap::{CommandFactory, Parser, value_parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{Device, DevicePipeline};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use vlanpb::{ShowDeviceVlanRequest, UpdateDeviceVlanRequest, device_vlan_service_client::DeviceVlanServiceClient};
use ync::{
    GlobalArgs,
    client::{ConnectionArgs, LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};
use ynpb::pb::{ListDevicesRequest, device_service_client::DeviceServiceClient};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod vlanpb {
    tonic::include_proto!("devices.vlan.controlplane.vlanpb.v1");
}

/// Manages vlan devices.
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
    /// Show the pipeline bindings and the vlan id of a device.
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
    /// VLAN id in 0..=4094, where 0 makes the device emit untagged frames.
    #[arg(long, value_parser = value_parser!(u16).range(0..=4094))]
    pub vlan: u16,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "devices.vlan.controlplane.vlanpb.v1.DeviceVlanService";

pub struct DeviceVlanService {
    service: Service<DeviceVlanServiceClient<LayeredChannel>>,
}

impl DeviceVlanService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            DeviceVlanServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn show_device(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let name = cmd.name;
        let request = ShowDeviceVlanRequest { name: name.clone() };

        let response = self
            .service
            .unary_with(
                "show",
                request,
                self.service.not_found("show", &format!("vlan device '{name}'")),
                async |client, request| client.show_device(request).await,
            )
            .await?;

        output::data(
            || &response,
            || {
                let rows = response.device.as_ref().map(binding_rows).unwrap_or_default();

                if output::is_colored() {
                    println!("{}: {}", output::paint_bold("VLAN"), response.vlan);
                } else {
                    println!("VLAN: {}", response.vlan);
                }

                if rows.is_empty() {
                    output::empty_with_hint(
                        format_args!("No pipeline bindings found for '{name}'."),
                        format_args!(
                            "bind one with 'yanet-cli device vlan update -n <name> -i <pipeline:weight> -o <pipeline:weight> --vlan <id>'"
                        ),
                    );
                    return;
                }

                display::print_table_from_entries(rows);
            },
        );

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let request = UpdateDeviceVlanRequest {
            name: cmd.name.clone(),
            device: Some(Device { input: cmd.input, output: cmd.output }),
            vlan: cmd.vlan as u32,
        };

        self.service
            .unary("update", request, async |client, request| {
                client.update_device(request).await
            })
            .await?;

        output::success("update", format_args!("Updated device '{}'.", cmd.name));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = DeviceVlanService::new(&cmd.globals.connection, action).await?;

    match cmd.mode {
        ModeCmd::Show(cmd) => service.show_device(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
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

/// Completion candidates for the device-name argument: the vlan devices the
/// device service currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn device_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, device_client, async move |mut client| {
        Ok(client
            .list(ListDevicesRequest {})
            .await?
            .into_inner()
            .ids
            .into_iter()
            .filter(|id| id.r#type == "vlan")
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
    use clap::{CommandFactory, error::ErrorKind};

    use super::*;

    #[test]
    fn test_cmd_is_valid() {
        Cmd::command().debug_assert();
    }

    /// Verifies that the verbosity flag still counts after the subcommand,
    /// where a short form of the vlan flag used to shadow it.
    #[test]
    fn test_update_verbosity_after_subcommand_counts() {
        let cmd = Cmd::try_parse_from(["yanet-cli-device-vlan", "update", "-n", "x", "--vlan", "5", "-vv"]).unwrap();

        assert_eq!(2, cmd.globals.verbose);
    }

    #[test]
    fn test_update_vlan_accepts_range_boundaries() {
        for (arg, expected) in [("0", 0), ("4094", 4094)] {
            let cmd = Cmd::try_parse_from(["yanet-cli-device-vlan", "update", "-n", "x", "--vlan", arg]).unwrap();
            let ModeCmd::Update(update) = cmd.mode else {
                panic!("expected ModeCmd::Update");
            };

            assert_eq!(expected, update.vlan);
        }
    }

    #[test]
    fn test_update_vlan_rejects_id_above_range() {
        let err = Cmd::try_parse_from(["yanet-cli-device-vlan", "update", "-n", "x", "--vlan", "4095"]).unwrap_err();

        assert_eq!(ErrorKind::ValueValidation, err.kind());
    }

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
