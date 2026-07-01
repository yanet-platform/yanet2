use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use code::{UpdateDevicePlainRequest, device_plain_service_client::DevicePlainServiceClient};
use commonpb::pb::Device;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod code {
    use serde::Serialize;

    tonic::include_proto!("devices.plain.controlplane.plainpb.v1");
}

/// DevicePlain module.
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
    Update(UpdateCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateCmd {
    /// The name of the device.
    #[arg(long, short)]
    pub name: String,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub input: Vec<String>,
    /// Pipeline assignments in format "pipeline_name:weight"
    #[arg(short, long)]
    pub output: Vec<String>,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "devices.plain.controlplane.plainpb.v1.DevicePlainService";

pub struct DevicePlainService {
    client: DevicePlainServiceClient<LayeredChannel>,
    endpoint: String,
}

impl DevicePlainService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = DevicePlainServiceClient::new(channel)
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

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
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

        let request = UpdateDevicePlainRequest {
            name: cmd.name.clone(),
            device: Some(Device { input, output }),
        };

        self.client
            .update_device(request)
            .await
            .map_err(self.map_err("update"))?;

        output::success("update", format_args!("Updated device {}.", cmd.name));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = DevicePlainService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
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
