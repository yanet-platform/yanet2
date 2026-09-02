use std::path::PathBuf;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::Device;
use tonic::codec::CompressionEncoding;
use trafgenpb::{
    ListConfigsRequest, SetRateRequest, ShowConfigRequest, UpdateDeviceRequest, UploadPcapRequest,
    trafgen_service_client::TrafgenServiceClient,
};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod trafgenpb {
    use serde::Serialize;

    tonic::include_proto!("devices.trafgen.controlplane.trafgenpb.v1");
}

/// Traffic generator device CLI.
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
    /// Bind the input/output pipelines of a generator.
    Update(UpdateCmd),
    /// List all generator configurations.
    List,
    /// Show a generator configuration.
    Show(ShowConfigCmd),
    /// Upload a pcap whose packets are replayed.
    Upload(UploadPcapCmd),
    /// Set the target aggregate packet rate.
    Rate(SetRateCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Update(..) => "update",
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Upload(..) => "upload",
            Self::Rate(..) => "rate",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Input pipeline assignments in "pipeline:weight" format.
    #[arg(long, short = 'i')]
    pub input: Vec<String>,
    /// Output pipeline assignments in "pipeline:weight" format.
    #[arg(long, short = 'o')]
    pub output: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UploadPcapCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Path to the pcap file whose packets are replayed.
    #[arg(value_name = "PATH")]
    pub pcap: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct SetRateCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Target aggregate packet rate in packets per second.
    #[arg(long, short = 'r')]
    pub rate: u64,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "devices.trafgen.controlplane.trafgenpb.v1.TrafgenService";

pub struct TrafgenService {
    service: Service<TrafgenServiceClient<LayeredChannel>>,
}

impl TrafgenService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            TrafgenServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn update_device(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
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

        let request = UpdateDeviceRequest {
            name: cmd.config_name.clone(),
            device: Some(Device { input, output }),
        };
        self.service
            .client()
            .update_device(request)
            .await
            .map_err(self.service.status("update"))?
            .into_inner();

        output::success("update", format_args!("Updated device {}.", cmd.config_name));

        Ok(())
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .client()
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No trafgen configurations found."),
                        format_args!("create one with 'yanet-cli-device-trafgen update --name <name>'"),
                    );
                    return;
                }

                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

        output::data(
            || &response,
            || {
                println!("rate (pps):  {}", response.rate_pps);
                println!("frame count: {}", response.frame_count);
                println!("total bytes: {}", response.total_bytes);
            },
        );

        Ok(())
    }

    pub async fn upload_pcap(&mut self, cmd: UploadPcapCmd) -> Result<(), Error> {
        let pcap = std::fs::read(&cmd.pcap).map_err(|err| {
            self.service
                .invalid("upload", format!("failed to read pcap {}: {err}", cmd.pcap.display()))
        })?;

        let request = UploadPcapRequest { name: cmd.config_name.clone(), pcap };
        self.service
            .client()
            .upload_pcap(request)
            .await
            .map_err(self.service.status("upload"))?
            .into_inner();

        output::success("upload", format_args!("Uploaded pcap to {}.", cmd.config_name));

        Ok(())
    }

    pub async fn set_rate(&mut self, cmd: SetRateCmd) -> Result<(), Error> {
        let request = SetRateRequest {
            name: cmd.config_name.clone(),
            rate_pps: cmd.rate,
        };
        self.service
            .client()
            .set_rate(request)
            .await
            .map_err(self.service.status("rate"))?
            .into_inner();

        output::success(
            "rate",
            format_args!("Set rate of {} to {} pps.", cmd.config_name, cmd.rate),
        );

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = TrafgenService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_device(cmd).await,
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Upload(cmd) => service.upload_pcap(cmd).await,
        ModeCmd::Rate(cmd) => service.set_rate(cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the generator device
/// configs the module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            TrafgenServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}
