use blackholepb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    blackhole_service_client::BlackholeServiceClient,
};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod blackholepb {
    use serde::Serialize;

    tonic::include_proto!("modules.blackhole.controlplane.blackholepb.v1");
}

/// Blackhole module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    List,
    Show(ShowConfigCmd),
    Update(UpdateConfigCmd),
    Delete(DeleteConfigCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Blackhole module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// Blackhole module name to create or replace.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// Blackhole module name to delete.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.blackhole.controlplane.blackholepb.v1.BlackholeService";

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

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = BlackholeService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
    }
}

pub struct BlackholeService {
    client: BlackholeServiceClient<LayeredChannel>,
    endpoint: String,
}

impl BlackholeService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = BlackholeServiceClient::new(channel)
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

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self
            .client
            .list_configs(request)
            .await
            .map_err(self.map_err("list"))?
            .into_inner();
        log::debug!("list configs response: {response:?}");

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
        log::trace!("show config request: {request:?}");
        let response = self
            .client
            .show_config(request)
            .await
            .map_err(self.map_err("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");

        output::data(&response, false, format_args!(""), || {
            println!("name: {}", response.name);
        });

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateConfigCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest { name: cmd.config_name.clone() };
        log::trace!("update config request: {request:?}");
        let response = self
            .client
            .update_config(request)
            .await
            .map_err(self.map_err("update"))?
            .into_inner();
        log::debug!("update config response: {response:?}");

        output::success("update", format_args!("Updated {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteConfigCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("delete config request: {request:?}");
        let response = self
            .client
            .delete_config(request)
            .await
            .map_err(self.map_err("delete"))?
            .into_inner();
        log::debug!("delete config response: {response:?}");

        output::success("delete", format_args!("Deleted {}.", cmd.config_name));

        Ok(())
    }
}
