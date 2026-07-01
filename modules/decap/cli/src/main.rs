use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use decappb::{
    ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, UpdateConfigRequest,
    decap_service_client::DecapServiceClient,
};
use netip::{Contiguous, IpNetwork};
use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod decappb {
    use serde::Serialize;

    tonic::include_proto!("modules.decap.controlplane.decappb.v1");
}

/// Decap module.
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
    List,
    Show(ShowConfigCmd),
    Update(UpdateConfigCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Decap module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// Decap module name to operate on.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Prefixes in the full desired set, replacing the current one entirely.
    #[arg(long, short)]
    pub prefixes: Vec<Contiguous<IpNetwork>>,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.decap.controlplane.decappb.v1.DecapService";

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
    let mut service = DecapService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
    }
}

pub struct DecapService {
    service: Service<DecapServiceClient<LayeredChannel>>,
}

impl DecapService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            DecapServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();
        log::debug!("list configs response: {response:?}");

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no decap configs"),
            || {
                let mut tree = TreeBuilder::new("List Decap Configs".to_string());
                for config in &response.configs {
                    tree.add_empty_child(config.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.to_owned() };
        log::trace!("show config request: {request:?}");
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");

        output::data(&response, false, format_args!(""), || print_tree(&response));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateConfigCmd) -> Result<(), Error> {
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            prefixes: cmd.prefixes.iter().map(|p| p.to_string()).collect(),
        };
        log::trace!("update config request: {request:?}");
        let response = self
            .service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?
            .into_inner();
        log::debug!("update config response: {response:?}");

        output::success("update", format_args!("Updated decap {}.", cmd.config_name));

        Ok(())
    }
}

fn print_tree(resp: &ShowConfigResponse) {
    let mut tree = TreeBuilder::new("Decap Prefixes".to_string());

    for (idx, prefix) in resp.prefixes.iter().enumerate() {
        tree.add_empty_child(format!("{idx}: {prefix}"));
    }

    let _ = ptree::print_tree(&tree.build());
}
