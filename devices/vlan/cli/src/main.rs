use clap::{ArgAction, Parser, value_parser};
use commonpb::pb::Device;
use tonic::codec::CompressionEncoding;
use vlanpb::{UpdateDeviceVlanRequest, device_vlan_service_client::DeviceVlanServiceClient};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod vlanpb {
    use serde::Serialize;

    tonic::include_proto!("devices.vlan.controlplane.vlanpb.v1");
}

/// DeviceVlan module.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    Update(UpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the device
    #[arg(long, short = 'n')]
    pub name: String,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(long, short = 'i')]
    pub input: Vec<String>,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(long, short = 'o')]
    pub output: Vec<String>,
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
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect_for(connection, "update", SERVICE_NAME, |channel| {
            DeviceVlanServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let input = cmd
            .input
            .into_iter()
            .map(|s| s.parse::<commonpb::pb::DevicePipeline>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(|err| self.service.invalid("update", err.to_string()))?;
        let output = cmd
            .output
            .into_iter()
            .map(|s| s.parse::<commonpb::pb::DevicePipeline>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(|err| self.service.invalid("update", err.to_string()))?;

        let request = UpdateDeviceVlanRequest {
            name: cmd.name.clone(),
            device: Some(Device { input, output }),
            vlan: cmd.vlan as u32,
        };

        self.service
            .client()
            .update_device(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated device {}.", cmd.name));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = DeviceVlanService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
    }
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
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

        assert_eq!(2, cmd.verbose);
    }

    #[test]
    fn test_update_vlan_accepts_range_boundaries() {
        for (arg, expected) in [("0", 0), ("4094", 4094)] {
            let cmd = Cmd::try_parse_from(["yanet-cli-device-vlan", "update", "-n", "x", "--vlan", arg]).unwrap();
            let ModeCmd::Update(update) = cmd.mode;

            assert_eq!(expected, update.vlan);
        }
    }

    #[test]
    fn test_update_vlan_rejects_id_above_range() {
        let err = Cmd::try_parse_from(["yanet-cli-device-vlan", "update", "-n", "x", "--vlan", "4095"]).unwrap_err();

        assert_eq!(ErrorKind::ValueValidation, err.kind());
    }
}
