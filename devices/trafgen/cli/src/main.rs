use std::path::PathBuf;

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::Device;
use tonic::codec::CompressionEncoding;
use trafgenpb::{
    ListConfigsRequest, SetRateRequest, ShowConfigRequest, UpdateDeviceRequest, UploadPcapRequest,
    trafgen_service_client::TrafgenServiceClient,
};
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod trafgenpb {
    use serde::Serialize;

    tonic::include_proto!("devices.trafgen.controlplane.trafgenpb.v1");
}

/// Traffic generator device CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
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

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Input pipeline assignments in "pipeline:weight" format.
    #[arg(short, long)]
    pub input: Vec<String>,
    /// Output pipeline assignments in "pipeline:weight" format.
    #[arg(short, long)]
    pub output: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UploadPcapCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Path to the pcap file whose packets are replayed.
    #[arg(long, short)]
    pub pcap: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct SetRateCmd {
    /// Generator device name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Target aggregate packet rate in packets per second.
    #[arg(long, short = 'r')]
    pub rate: u64,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "devices.trafgen.controlplane.trafgenpb.v1.TrafgenService";

pub struct TrafgenService {
    client: TrafgenServiceClient<LayeredChannel>,
    endpoint: String,
}

impl TrafgenService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = TrafgenServiceClient::new(channel)
            .max_decoding_message_size(256 * 1024 * 1024)
            .max_encoding_message_size(256 * 1024 * 1024)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            endpoint: connection.endpoint.clone(),
        })
    }

    fn map_err<'a>(&'a self, action: &'a str) -> impl FnOnce(tonic::Status) -> Error + 'a {
        let endpoint = self.endpoint.clone();
        move |status| Error::from_status(status, action, endpoint, SERVICE_NAME)
    }

    fn invalid_argument(&self, action: &'static str, message: String) -> Error {
        Error::from_status(
            tonic::Status::invalid_argument(message),
            action,
            self.endpoint.clone(),
            SERVICE_NAME,
        )
    }

    pub async fn update_device(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let input = cmd
            .input
            .into_iter()
            .map(|s| s.parse::<commonpb::pb::DevicePipeline>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(|err| self.invalid_argument("update", err.to_string()))?;
        let output = cmd
            .output
            .into_iter()
            .map(|s| s.parse::<commonpb::pb::DevicePipeline>())
            .collect::<Result<Vec<_>, _>>()
            .map_err(|err| self.invalid_argument("update", err.to_string()))?;

        let request = UpdateDeviceRequest {
            name: cmd.config_name.clone(),
            device: Some(Device { input, output }),
        };
        self.client
            .update_device(request)
            .await
            .map_err(self.map_err("update"))?
            .into_inner();

        output::success("update", format_args!("Updated device {}.", cmd.config_name));

        Ok(())
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .client
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.map_err("list"))?
            .into_inner();

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no configurations"),
            || {
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
            .client
            .show_config(request)
            .await
            .map_err(self.map_err("show"))?
            .into_inner();

        output::data(&response, false, format_args!(""), || {
            println!("rate (pps):  {}", response.rate_pps);
            println!("frame count: {}", response.frame_count);
            println!("total bytes: {}", response.total_bytes);
        });

        Ok(())
    }

    pub async fn upload_pcap(&mut self, cmd: UploadPcapCmd) -> Result<(), Error> {
        let pcap = std::fs::read(&cmd.pcap).map_err(|err| {
            self.invalid_argument("upload", format!("failed to read pcap {}: {err}", cmd.pcap.display()))
        })?;

        let request = UploadPcapRequest { name: cmd.config_name.clone(), pcap };
        self.client
            .upload_pcap(request)
            .await
            .map_err(self.map_err("upload"))?
            .into_inner();

        output::success("upload", format_args!("Uploaded pcap to {}.", cmd.config_name));

        Ok(())
    }

    pub async fn set_rate(&mut self, cmd: SetRateCmd) -> Result<(), Error> {
        let request = SetRateRequest {
            name: cmd.config_name.clone(),
            rate_pps: cmd.rate,
        };
        self.client
            .set_rate(request)
            .await
            .map_err(self.map_err("rate"))?
            .into_inner();

        output::success(
            "rate",
            format_args!("Set rate of {} to {} pps.", cmd.config_name, cmd.rate),
        );

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = TrafgenService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_device(cmd).await,
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Upload(cmd) => service.upload_pcap(cmd).await,
        ModeCmd::Rate(cmd) => service.set_rate(cmd).await,
    }
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}
