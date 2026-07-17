use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use l3bpb::{
    CreateServiceRequest, DeleteServiceRequest, ListModuleConfigsRequest, ListServicesRequest, ModuleConfig,
    UpdateModuleConfigRequest, UpdateRealServerStateRequest, UpdateRealServerWeightRequest, UpdateServiceRequest,
    VirtualService, l3b_service_client::L3bServiceClient,
};
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(non_snake_case)]
pub mod l3bpb {
    use serde::Serialize;

    tonic::include_proto!("modules.l3b.controlplane.l3bpb.v1");
}

/// L3b module.
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
    /// Create a named virtual service.
    CreateService(ServiceCmd),
    /// Replace a named virtual service.
    UpdateService(ServiceCmd),
    /// Delete a named virtual service.
    DeleteService(NameCmd),
    /// List virtual service names.
    ListServices,
    /// Install a named module configuration.
    UpdateModuleConfig(ModuleConfigCmd),
    /// List module configuration names.
    ListModuleConfigs,
    /// Enable or disable a real server within a named virtual service.
    UpdateRealServerState(RealServerCmd),
    /// Set the weight of a real server within a named virtual service.
    UpdateRealServerWeight(WeightCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct ServiceCmd {
    /// Virtual service name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
    /// Scheduler hash mask.
    #[arg(long, default_value_t = 0)]
    pub hash_mask: u32,
    /// Scheduler index mask.
    #[arg(long, default_value_t = 0)]
    pub index_mask: u32,
}

#[derive(Debug, Clone, Parser)]
pub struct NameCmd {
    /// Name to operate on.
    #[arg(long = "name", short = 'n')]
    pub name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct ModuleConfigCmd {
    /// Module configuration name.
    #[arg(long = "name", short = 'n')]
    pub name: String,
    /// Names of the virtual services to install, in index order.
    #[arg(long = "service", value_name = "SERVICE")]
    pub services: Vec<String>,
}

#[derive(Debug, Clone, Parser)]
pub struct RealServerCmd {
    /// Virtual service name.
    #[arg(long = "name", short = 'n')]
    pub service: String,
    /// Index of the real server within the service.
    #[arg(long = "index")]
    pub real_server_index: u32,
    /// Whether the real server is enabled.
    #[arg(long)]
    pub enabled: bool,
}

#[derive(Debug, Clone, Parser)]
pub struct WeightCmd {
    /// Virtual service name.
    #[arg(long = "name", short = 'n')]
    pub service: String,
    /// Index of the real server within the service.
    #[arg(long = "index")]
    pub real_server_index: u32,
    /// New weight of the real server.
    #[arg(long)]
    pub weight: u32,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.l3b.controlplane.l3bpb.v1.L3bService";

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
    let mut service = L3BService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::CreateService(cmd) => service.create_service(cmd).await,
        ModeCmd::UpdateService(cmd) => service.update_service(cmd).await,
        ModeCmd::DeleteService(cmd) => service.delete_service(cmd).await,
        ModeCmd::ListServices => service.list_services().await,
        ModeCmd::UpdateModuleConfig(cmd) => service.update_module_config(cmd).await,
        ModeCmd::ListModuleConfigs => service.list_module_configs().await,
        ModeCmd::UpdateRealServerState(cmd) => service.update_real_server_state(cmd).await,
        ModeCmd::UpdateRealServerWeight(cmd) => service.update_real_server_weight(cmd).await,
    }
}

pub struct L3BService {
    client: L3bServiceClient<LayeredChannel>,
    endpoint: String,
}

impl L3BService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = L3bServiceClient::new(channel)
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

    pub async fn create_service(&mut self, cmd: ServiceCmd) -> Result<(), Error> {
        let request = CreateServiceRequest {
            service: Some(VirtualService {
                name: cmd.name.clone(),
                hash_mask: cmd.hash_mask,
                index_mask: cmd.index_mask,
                ..Default::default()
            }),
        };
        log::trace!("create service request: {request:?}");
        self.client
            .create_service(request)
            .await
            .map_err(self.map_err("create-service"))?;
        output::success("create-service", format_args!("Created service {}.", cmd.name));
        Ok(())
    }

    pub async fn update_service(&mut self, cmd: ServiceCmd) -> Result<(), Error> {
        let request = UpdateServiceRequest {
            service: Some(VirtualService {
                name: cmd.name.clone(),
                hash_mask: cmd.hash_mask,
                index_mask: cmd.index_mask,
                ..Default::default()
            }),
        };
        log::trace!("update service request: {request:?}");
        self.client
            .update_service(request)
            .await
            .map_err(self.map_err("update-service"))?;
        output::success("update-service", format_args!("Updated service {}.", cmd.name));
        Ok(())
    }

    pub async fn delete_service(&mut self, cmd: NameCmd) -> Result<(), Error> {
        let request = DeleteServiceRequest { name: cmd.name.clone() };
        log::trace!("delete service request: {request:?}");
        self.client
            .delete_service(request)
            .await
            .map_err(self.map_err("delete-service"))?;
        output::success("delete-service", format_args!("Deleted service {}.", cmd.name));
        Ok(())
    }

    pub async fn list_services(&mut self) -> Result<(), Error> {
        let request = ListServicesRequest {};
        log::trace!("list services request: {request:?}");
        let response = self
            .client
            .list_services(ListServicesRequest {})
            .await
            .map_err(self.map_err("list-services"))?
            .into_inner();
        log::debug!("list services response: {response:?}");

        output::data(
            &response.services,
            response.services.is_empty(),
            format_args!("no services"),
            || {
                for name in &response.services {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn update_module_config(&mut self, cmd: ModuleConfigCmd) -> Result<(), Error> {
        let request = UpdateModuleConfigRequest {
            config: Some(ModuleConfig {
                name: cmd.name.clone(),
                services: cmd.services.clone(),
                ..Default::default()
            }),
        };
        log::trace!("update module config request: {request:?}");
        self.client
            .update_module_config(request)
            .await
            .map_err(self.map_err("update-module-config"))?;
        output::success(
            "update-module-config",
            format_args!("Updated module config {}.", cmd.name),
        );
        Ok(())
    }

    pub async fn list_module_configs(&mut self) -> Result<(), Error> {
        let request = ListModuleConfigsRequest {};
        log::trace!("list module configs request: {request:?}");
        let response = self
            .client
            .list_module_configs(ListModuleConfigsRequest {})
            .await
            .map_err(self.map_err("list-module-configs"))?
            .into_inner();
        log::debug!("list module configs response: {response:?}");

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no module configs"),
            || {
                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn update_real_server_state(&mut self, cmd: RealServerCmd) -> Result<(), Error> {
        let request = UpdateRealServerStateRequest {
            service: cmd.service.clone(),
            real_server_index: cmd.real_server_index,
            enabled: cmd.enabled,
        };
        log::trace!("update real server state request: {request:?}");
        self.client
            .update_real_server_state(request)
            .await
            .map_err(self.map_err("update-real-server-state"))?;
        output::success(
            "update-real-server-state",
            format_args!(
                "Set real server {} state of {} to {}.",
                cmd.real_server_index, cmd.service, cmd.enabled
            ),
        );
        Ok(())
    }

    pub async fn update_real_server_weight(&mut self, cmd: WeightCmd) -> Result<(), Error> {
        let request = UpdateRealServerWeightRequest {
            service: cmd.service.clone(),
            real_server_index: cmd.real_server_index,
            weight: cmd.weight,
        };
        log::trace!("update real server weight request: {request:?}");
        self.client
            .update_real_server_weight(request)
            .await
            .map_err(self.map_err("update-real-server-weight"))?;
        output::success(
            "update-real-server-weight",
            format_args!(
                "Set real server {} weight of {} to {}.",
                cmd.real_server_index, cmd.service, cmd.weight
            ),
        );
        Ok(())
    }
}
