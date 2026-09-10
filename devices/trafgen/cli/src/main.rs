use std::path::PathBuf;

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{Device, DevicePipeline};
use tonic::codec::CompressionEncoding;
use trafgenpb::{
    ListConfigsRequest, SetRateRequest, ShowConfigRequest, UpdateDeviceRequest, UploadPcapRequest,
    trafgen_service_client::TrafgenServiceClient,
};
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod trafgenpb {
    tonic::include_proto!("devices.trafgen.controlplane.trafgenpb.v1");
}

/// Manages trafgen devices.
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
    pub input: Vec<DevicePipeline>,
    /// Output pipeline assignments in "pipeline:weight" format.
    #[arg(long, short = 'o')]
    pub output: Vec<DevicePipeline>,
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

fn client(channel: LayeredChannel) -> TrafgenServiceClient<LayeredChannel> {
    TrafgenServiceClient::new(channel)
        .max_decoding_message_size(256 * 1024 * 1024)
        .max_encoding_message_size(256 * 1024 * 1024)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

type TrafgenService = Service<TrafgenServiceClient<LayeredChannel>>;

async fn update_device(service: &mut TrafgenService, cmd: UpdateCmd) -> Result<(), Error> {
    let request = UpdateDeviceRequest {
        name: cmd.config_name.clone(),
        device: Some(Device { input: cmd.input, output: cmd.output }),
    };
    service
        .unary("update", request, async |client, request| {
            client.update_device(request).await
        })
        .await?;

    output::success("update", format_args!("Updated device '{}'.", cmd.config_name));

    Ok(())
}

async fn list_configs(service: &mut TrafgenService) -> Result<(), Error> {
    let response = service
        .unary("list", ListConfigsRequest {}, async |client, request| {
            client.list_configs(request).await
        })
        .await?;

    output::data(
        || &response.configs,
        || {
            display::print_names_with_hint(
                &response.configs,
                format_args!("No trafgen configurations found."),
                format_args!(
                    "create one with 'yanet-cli-device-trafgen update --name <name> --input <pipeline:weight> --output <pipeline:weight>'"
                ),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut TrafgenService, cmd: ShowConfigCmd) -> Result<(), Error> {
    let request = ShowConfigRequest { name: cmd.config_name.clone() };
    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("device '{}'", cmd.config_name)),
            async |client, request| client.show_config(request).await,
        )
        .await?;

    output::data(
        || &response,
        || {
            display::KeyValue::new()
                .row("rate (pps)", response.rate_pps)
                .row("frame count", response.frame_count)
                .row("total bytes", response.total_bytes)
                .print()
        },
    );

    Ok(())
}

async fn upload_pcap(service: &mut TrafgenService, cmd: UploadPcapCmd) -> Result<(), Error> {
    let pcap = std::fs::read(&cmd.pcap)
        .map_err(|err| service.invalid("upload", format!("failed to read pcap {}: {err}", cmd.pcap.display())))?;

    // The capture itself must never reach the log, so the upload
    // bypasses the logged call path.
    let request = UploadPcapRequest { name: cmd.config_name.clone(), pcap };
    service
        .client()
        .upload_pcap(request)
        .await
        .map_err(service.status("upload"))?;

    output::success("upload", format_args!("Uploaded pcap to device '{}'.", cmd.config_name));

    Ok(())
}

async fn set_rate(service: &mut TrafgenService, cmd: SetRateCmd) -> Result<(), Error> {
    let request = SetRateRequest {
        name: cmd.config_name.clone(),
        rate_pps: cmd.rate,
    };
    service
        .unary("rate", request, async |client, request| client.set_rate(request).await)
        .await?;

    output::success(
        "rate",
        format_args!("Set rate on device '{}' to {} pps.", cmd.config_name, cmd.rate),
    );

    Ok(())
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => update_device(&mut service, cmd).await,
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
        ModeCmd::Upload(cmd) => upload_pcap(&mut service, cmd).await,
        ModeCmd::Rate(cmd) => set_rate(&mut service, cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Completion candidates for a `--name` argument: the generator device
/// configs the module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}
