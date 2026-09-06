use core::net::IpAddr;
use std::path::{Path, PathBuf};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use l3bpb::{
    CreateServiceRequest, DeleteServiceRequest, ListModuleConfigsRequest, ListServicesRequest, ModuleConfig,
    UpdateModuleConfigRequest, UpdateRealServerStateRequest, UpdateRealServerWeightRequest, UpdateServiceRequest,
    VirtualService, l3b_service_client::L3bServiceClient,
};
use serde::Deserialize;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod l3bpb {
    use serde::Serialize;

    tonic::include_proto!("modules.l3b.controlplane.l3bpb.v1");
}

/// L3b module.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
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

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::CreateService(..) => "create-service",
            Self::UpdateService(..) => "update-service",
            Self::DeleteService(..) => "delete-service",
            Self::ListServices => "list-services",
            Self::UpdateModuleConfig(..) => "update-module-config",
            Self::ListModuleConfigs => "list-module-configs",
            Self::UpdateRealServerState(..) => "update-real-server-state",
            Self::UpdateRealServerWeight(..) => "update-real-server-weight",
        }
    }
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
    /// YAML document with the real servers and source filter rules.
    #[arg(long = "file", value_name = "PATH")]
    pub file: Option<PathBuf>,
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
    /// YAML document with the destination filter rules; each rule names the
    /// virtual service it routes to.
    #[arg(long = "file", value_name = "PATH")]
    pub file: Option<PathBuf>,
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

/// YAML document populating a virtual service: its real servers and the
/// source filter rules gating its traffic.
///
/// `hash-mask`/`index-mask` stay command flags; the document carries only
/// what has no natural flag form.
#[derive(Debug, Deserialize)]
struct ServiceDocument {
    /// Real servers the service dispatches to.
    real_servers: Vec<RealServerDoc>,
    /// Source-side match rules for incoming traffic.
    source_filter_rules: Vec<SourceFilterRuleDoc>,
    /// Hash index size of the service's session table; the default when
    /// omitted.
    #[serde(default)]
    session_index_size: u32,
}

/// One real server tunnel endpoint.
#[derive(Debug, Deserialize)]
struct RealServerDoc {
    /// Tunnel destination address of the real server.
    destination_address: IpAddr,
    /// Source network the outer source address is derived from.
    source_network: filterpb::pb::IpNet,
}

/// One source-side match rule.
#[derive(Debug, Deserialize)]
struct SourceFilterRuleDoc {
    /// IPv6 source networks the rule accepts.
    net6s: Vec<filterpb::pb::IpNet>,
    /// IPv4 source networks the rule accepts.
    net4s: Vec<filterpb::pb::IpNet>,
    /// Destination port ranges the rule accepts.
    port_ranges: Vec<RangeDoc>,
}

/// An inclusive numeric range.
#[derive(Debug, Deserialize)]
struct RangeDoc {
    from: u16,
    to: u16,
}

/// YAML document populating a module configuration: the destination filter
/// rules routing traffic to services; the linked services are exactly those
/// the rules name.
#[derive(Debug, Deserialize)]
struct ModuleConfigDocument {
    /// Destination rules, in rule order.
    destination_filter_rules: Vec<DestinationRuleDoc>,
}

/// One destination-side classification rule.
#[derive(Debug, Deserialize)]
struct DestinationRuleDoc {
    /// IPv6 destination networks the rule matches.
    net6s: Vec<filterpb::pb::IpNet>,
    /// IPv4 destination networks the rule matches.
    net4s: Vec<filterpb::pb::IpNet>,
    /// Transport protocol ranges the rule matches.
    proto_ranges: Vec<ProtoRangeDoc>,
    /// Name of the virtual service matched traffic is routed to.
    service: String,
}

/// A transport protocol plus an optional subtype byte range.
///
/// `tcp` with the default subtypes selects every TCP packet regardless of
/// flags; a number selects that protocol directly.
#[derive(Debug, Deserialize)]
struct ProtoRangeDoc {
    /// The transport protocol.
    proto: ProtocolDoc,
    /// First matched subtype byte; defaults to 0.
    #[serde(default)]
    subtype_from: Option<u8>,
    /// Last matched subtype byte; defaults to 255.
    #[serde(default)]
    subtype_to: Option<u8>,
}

/// A transport protocol by name or number.
#[derive(Debug, Deserialize)]
#[serde(untagged)]
enum ProtocolDoc {
    /// A well-known protocol name.
    Named(NamedProtocol),
    /// A protocol number.
    Number(u8),
}

/// A well-known transport protocol name.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "lowercase")]
enum NamedProtocol {
    /// Transmission Control Protocol.
    Tcp,
    /// User Datagram Protocol.
    Udp,
}

impl From<&RangeDoc> for filterpb::pb::PortRange {
    fn from(range: &RangeDoc) -> Self {
        Self {
            from: u32::from(range.from),
            to: u32::from(range.to),
        }
    }
}

impl From<&ProtoRangeDoc> for filterpb::pb::ProtoRange {
    fn from(range: &ProtoRangeDoc) -> Self {
        let proto = match range.proto {
            ProtocolDoc::Named(NamedProtocol::Tcp) => 6,
            ProtocolDoc::Named(NamedProtocol::Udp) => 17,
            ProtocolDoc::Number(number) => number,
        };
        let subtype_from = u32::from(range.subtype_from.unwrap_or(0));
        let subtype_to = u32::from(range.subtype_to.unwrap_or(u8::MAX));
        Self {
            from: (u32::from(proto) << 8) | subtype_from,
            to: (u32::from(proto) << 8) | subtype_to,
        }
    }
}

impl From<&RealServerDoc> for l3bpb::RealServer {
    fn from(server: &RealServerDoc) -> Self {
        let address = match server.destination_address {
            IpAddr::V4(address) => address.octets().to_vec(),
            IpAddr::V6(address) => address.octets().to_vec(),
        };
        Self {
            destination_address: address,
            source_network: Some(server.source_network.clone()),
        }
    }
}

impl From<&SourceFilterRuleDoc> for l3bpb::SourceFilterRule {
    fn from(rule: &SourceFilterRuleDoc) -> Self {
        Self {
            net6s: rule.net6s.clone(),
            net4s: rule.net4s.clone(),
            port_ranges: rule.port_ranges.iter().map(Into::into).collect(),
        }
    }
}

impl From<&DestinationRuleDoc> for l3bpb::DestinationFilterRule {
    fn from(rule: &DestinationRuleDoc) -> Self {
        Self {
            net6s: rule.net6s.clone(),
            net4s: rule.net4s.clone(),
            proto_ranges: rule.proto_ranges.iter().map(Into::into).collect(),
            service: rule.service.clone(),
        }
    }
}

/// Reads and parses a YAML document from a file, attributing failures to the
/// given verb as invalid input.
#[allow(clippy::result_large_err)]
fn load_document<T: for<'de> Deserialize<'de>>(
    path: &Path,
    verb: &'static str,
    service: &L3BService,
) -> Result<T, Error> {
    let file = std::fs::File::open(path)
        .map_err(|err| service.invalid(verb, format!("failed to open {}: {err}", path.display())))?;
    serde_yaml::from_reader(file)
        .map_err(|err| service.invalid(verb, format!("invalid document {}: {err}", path.display())))
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
    let action = cmd.mode.action();
    let mut service = L3BService::new(&cmd.connection, action).await?;

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
    service: Service<L3bServiceClient<LayeredChannel>>,
}

impl L3BService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            L3bServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    fn invalid(&self, verb: &'static str, message: String) -> Error {
        self.service.invalid(verb, message)
    }

    pub async fn create_service(&mut self, cmd: ServiceCmd) -> Result<(), Error> {
        let service = self.virtual_service(&cmd).await?;
        let request = CreateServiceRequest { service: Some(service) };
        log::trace!("create service request: {request:?}");
        self.service
            .client()
            .create_service(request)
            .await
            .map_err(self.service.status("create-service"))?;
        output::success("create-service", format_args!("Created service {}.", cmd.name));
        Ok(())
    }

    pub async fn update_service(&mut self, cmd: ServiceCmd) -> Result<(), Error> {
        let service = self.virtual_service(&cmd).await?;
        let request = UpdateServiceRequest { service: Some(service) };
        log::trace!("update service request: {request:?}");
        self.service
            .client()
            .update_service(request)
            .await
            .map_err(self.service.status("update-service"))?;
        output::success("update-service", format_args!("Updated service {}.", cmd.name));
        Ok(())
    }

    /// Builds a virtual service request from the flags, populating the real
    /// servers and source filter rules from the document when one is given.
    async fn virtual_service(&mut self, cmd: &ServiceCmd) -> Result<VirtualService, Error> {
        let mut service = VirtualService {
            name: cmd.name.clone(),
            hash_mask: cmd.hash_mask,
            index_mask: cmd.index_mask,
            ..Default::default()
        };

        if let Some(path) = &cmd.file {
            let document: ServiceDocument = load_document(path, "create-service", self)?;
            service.real_servers = document.real_servers.iter().map(Into::into).collect();
            service.source_filter_rules = document.source_filter_rules.iter().map(Into::into).collect();
            service.session_index_size = document.session_index_size;
        }

        Ok(service)
    }

    pub async fn delete_service(&mut self, cmd: NameCmd) -> Result<(), Error> {
        let request = DeleteServiceRequest { name: cmd.name.clone() };
        log::trace!("delete service request: {request:?}");
        self.service
            .client()
            .delete_service(request)
            .await
            .map_err(self.service.status("delete-service"))?;
        output::success("delete-service", format_args!("Deleted service {}.", cmd.name));
        Ok(())
    }

    pub async fn list_services(&mut self) -> Result<(), Error> {
        let request = ListServicesRequest {};
        log::trace!("list services request: {request:?}");
        let response = self
            .service
            .client()
            .list_services(ListServicesRequest {})
            .await
            .map_err(self.service.status("list-services"))?
            .into_inner();
        log::debug!("list services response: {response:?}");

        output::data(
            || &response.services,
            || {
                if response.services.is_empty() {
                    output::empty(format_args!("no services"));
                    return;
                }
                for name in &response.services {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn update_module_config(&mut self, cmd: ModuleConfigCmd) -> Result<(), Error> {
        let mut config = ModuleConfig {
            name: cmd.name.clone(),
            ..Default::default()
        };

        if let Some(path) = &cmd.file {
            let document: ModuleConfigDocument = load_document(path, "update-module-config", self)?;
            config.destination_filter_rules = document.destination_filter_rules.iter().map(Into::into).collect();
        }

        let request = UpdateModuleConfigRequest { config: Some(config) };
        log::trace!("update module config request: {request:?}");
        self.service
            .client()
            .update_module_config(request)
            .await
            .map_err(self.service.status("update-module-config"))?;
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
            .service
            .client()
            .list_module_configs(ListModuleConfigsRequest {})
            .await
            .map_err(self.service.status("list-module-configs"))?
            .into_inner();
        log::debug!("list module configs response: {response:?}");

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty(format_args!("no module configs"));
                    return;
                }
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
        self.service
            .client()
            .update_real_server_state(request)
            .await
            .map_err(self.service.status("update-real-server-state"))?;
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
        self.service
            .client()
            .update_real_server_weight(request)
            .await
            .map_err(self.service.status("update-real-server-weight"))?;
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
