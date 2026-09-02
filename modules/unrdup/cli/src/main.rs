use core::{error::Error as StdError, net::IpAddr};
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::IpAddress;
use filterpb::pb::IpNet;
use serde::{Deserialize, Serialize};
use tonic::codec::CompressionEncoding;
use unrduppb::{
    Config, DeleteConfigRequest, Endpoint, ListConfigsRequest, Protocol, Service, ShowConfigRequest,
    UpdateConfigRequest, unrdup_service_client::UnrdupServiceClient,
};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service as GrpcService},
    completion,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod unrduppb {
    use serde::Serialize;

    tonic::include_proto!("modules.unrdup.controlplane.unrduppb.v1");
}

/// Unrdup module.
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
    List,
    Show(ShowConfigCmd),
    Update(UpdateConfigCmd),
    /// Delete an unrdup module config.
    Delete(DeleteConfigCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::List => "list",
            Self::Show(..) => "show",
            Self::Update(..) => "update",
            Self::Delete(..) => "delete",
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct ShowConfigCmd {
    /// Unrdup module name to operate on.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct UpdateConfigCmd {
    /// Unrdup module name to create or replace.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Path to the YAML file describing the whole configuration.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct DeleteConfigCmd {
    /// Unrdup module name to delete.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

/// Transport a virtual service serves.
#[derive(Debug, Clone, Copy, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum TransportProto {
    Tcp,
    Udp,
}

impl From<TransportProto> for Protocol {
    fn from(value: TransportProto) -> Self {
        match value {
            TransportProto::Tcp => Protocol::Tcp,
            TransportProto::Udp => Protocol::Udp,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EndpointConfig {
    pub port: u16,
    pub proto: TransportProto,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceConfig {
    pub vip: IpAddr,
    pub peers: Vec<IpAddr>,
    pub endpoints: Vec<EndpointConfig>,
}

/// The YAML form of a whole module configuration.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct UnrdupConfig {
    #[serde(default)]
    pub source_v4: Option<IpNet>,
    #[serde(default)]
    pub source_v6: Option<IpNet>,
    #[serde(default)]
    pub services: Vec<ServiceConfig>,
}

impl UnrdupConfig {
    pub fn load(path: &Path) -> Result<Self, Box<dyn StdError>> {
        let file = File::open(path)?;
        let config = serde_yaml::from_reader(file)?;

        Ok(config)
    }
}

impl From<UnrdupConfig> for Config {
    fn from(value: UnrdupConfig) -> Self {
        Self {
            source_v4: value.source_v4,
            source_v6: value.source_v6,
            services: value.services.into_iter().map(Service::from).collect(),
        }
    }
}

impl From<ServiceConfig> for Service {
    fn from(value: ServiceConfig) -> Self {
        Self {
            vip: Some(addr_to_proto(value.vip)),
            peers: value.peers.into_iter().map(addr_to_proto).collect(),
            endpoints: value.endpoints.into_iter().map(Endpoint::from).collect(),
        }
    }
}

impl From<EndpointConfig> for Endpoint {
    fn from(value: EndpointConfig) -> Self {
        Self {
            port: u32::from(value.port),
            protocol: Protocol::from(value.proto) as i32,
        }
    }
}

impl TryFrom<Config> for UnrdupConfig {
    type Error = Box<dyn StdError>;

    fn try_from(value: Config) -> Result<Self, Self::Error> {
        Ok(Self {
            source_v4: value.source_v4,
            source_v6: value.source_v6,
            services: value
                .services
                .into_iter()
                .map(ServiceConfig::try_from)
                .collect::<Result<_, _>>()?,
        })
    }
}

impl TryFrom<Service> for ServiceConfig {
    type Error = Box<dyn StdError>;

    fn try_from(value: Service) -> Result<Self, Self::Error> {
        let vip = value.vip.as_ref().ok_or("service carries no vip")?;

        Ok(Self {
            vip: IpAddr::try_from(vip)?,
            peers: value.peers.iter().map(IpAddr::try_from).collect::<Result<_, _>>()?,
            endpoints: value
                .endpoints
                .into_iter()
                .map(EndpointConfig::try_from)
                .collect::<Result<_, _>>()?,
        })
    }
}

impl TryFrom<Endpoint> for EndpointConfig {
    type Error = Box<dyn StdError>;

    fn try_from(value: Endpoint) -> Result<Self, Self::Error> {
        Ok(Self {
            port: u16::try_from(value.port)?,
            proto: TransportProto::try_from(value.protocol())?,
        })
    }
}

impl TryFrom<Protocol> for TransportProto {
    type Error = Box<dyn StdError>;

    fn try_from(value: Protocol) -> Result<Self, Self::Error> {
        match value {
            Protocol::Tcp => Ok(Self::Tcp),
            Protocol::Udp => Ok(Self::Udp),
            Protocol::Unspecified => Err("endpoint protocol is unspecified".into()),
        }
    }
}

fn addr_to_proto(addr: IpAddr) -> IpAddress {
    let bytes = match addr {
        IpAddr::V4(addr) => addr.octets().to_vec(),
        IpAddr::V6(addr) => addr.octets().to_vec(),
    };

    IpAddress { addr: bytes }
}

const SERVICE_NAME: &str = "modules.unrdup.controlplane.unrduppb.v1.UnrdupService";

/// Maps a genuine "config not found" status into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "requested config");

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = UnrdupService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
    }
}

pub struct UnrdupService {
    service: GrpcService<UnrdupServiceClient<LayeredChannel>>,
}

impl UnrdupService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = GrpcService::connect_for(connection, action, SERVICE_NAME, |channel| {
            UnrdupServiceClient::new(channel)
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
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No unrdup configurations found."),
                        format_args!("create one with 'yanet-cli-unrdup update --name <name> <path>'"),
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
        log::trace!("show config request: {request:?}");
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");

        let config = response
            .config
            .ok_or_else(|| self.service.invalid("show", "response carries no config"))?;

        let rendered =
            UnrdupConfig::try_from(config.clone()).map_err(|err| self.service.invalid("show", err.to_string()))?;

        output::data(
            || &config,
            || {
                println!(
                    "{}",
                    serde_yaml::to_string(&rendered).expect("unrdup config YAML serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateConfigCmd) -> Result<(), Error> {
        let config = UnrdupConfig::load(&cmd.file).map_err(|err| self.service.invalid("update", err.to_string()))?;

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            config: Some(Config::from(config)),
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

        output::success("update", format_args!("Updated {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteConfigCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("delete config request: {request:?}");
        let response = self
            .service
            .client()
            .delete_config(request)
            .await
            .map_err(|status| {
                NOT_FOUND.map(
                    status,
                    "delete",
                    self.service.endpoint(),
                    Some(&format!("config '{}'", cmd.config_name)),
                )
            })?
            .into_inner();
        log::debug!("delete config response: {response:?}");

        output::success("delete", format_args!("Deleted unrdup {}.", cmd.config_name));

        Ok(())
    }
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            UnrdupServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    const SAMPLE: &str = r#"
source_v4: "10.0.0.0/30"
source_v6: "2001:db8:a::/96"
services:
  - vip: "192.0.2.1"
    peers:
      - "10.0.0.10"
      - "2001:db8:b::11"
    endpoints:
      - { port: 443, proto: tcp }
      - { port: 53, proto: udp }
"#;

    #[test]
    fn yaml_becomes_a_request() {
        let parsed: UnrdupConfig = serde_yaml::from_str(SAMPLE).expect("sample must parse");
        let config = Config::from(parsed);

        let source_v4 = config.source_v4.expect("source_v4 must be set");
        assert_eq!(vec![10, 0, 0, 0], source_v4.addr);
        assert_eq!(vec![255, 255, 255, 252], source_v4.mask);

        let source_v6 = config.source_v6.expect("source_v6 must be set");
        assert_eq!(16, source_v6.addr.len());
        assert_eq!(12, source_v6.mask.iter().filter(|byte| **byte == 0xff).count());

        assert_eq!(1, config.services.len());
        let service = &config.services[0];

        assert_eq!(vec![192, 0, 2, 1], service.vip.as_ref().expect("vip").addr);
        assert_eq!(2, service.peers.len());
        assert_eq!(4, service.peers[0].addr.len());
        assert_eq!(16, service.peers[1].addr.len());

        assert_eq!(443, service.endpoints[0].port);
        assert_eq!(Protocol::Tcp as i32, service.endpoints[0].protocol);
        assert_eq!(53, service.endpoints[1].port);
        assert_eq!(Protocol::Udp as i32, service.endpoints[1].protocol);
    }

    #[test]
    fn a_response_round_trips_into_the_input_form() {
        let parsed: UnrdupConfig = serde_yaml::from_str(SAMPLE).expect("sample must parse");
        let rendered = serde_yaml::to_string(&parsed).expect("must serialize");

        let wire = Config::from(parsed);
        let back = UnrdupConfig::try_from(wire).expect("wire form must convert back");

        assert_eq!(rendered, serde_yaml::to_string(&back).expect("must serialize"));

        let reparsed: UnrdupConfig = serde_yaml::from_str(&rendered).expect("printed output must parse as input");
        assert_eq!(rendered, serde_yaml::to_string(&reparsed).expect("must serialize"));
    }

    #[test]
    fn an_unspecified_protocol_is_rejected() {
        let endpoint = Endpoint {
            port: 443,
            protocol: Protocol::Unspecified as i32,
        };

        assert!(EndpointConfig::try_from(endpoint).is_err());
    }

    #[test]
    fn a_family_may_have_no_source() {
        let parsed: UnrdupConfig = serde_yaml::from_str("services: []").expect("an empty config must parse");
        let config = Config::from(parsed);

        assert!(config.source_v4.is_none());
        assert!(config.source_v6.is_none());
    }

    #[test]
    fn an_invalid_prefix_is_rejected() {
        let result: Result<UnrdupConfig, _> = serde_yaml::from_str("source_v4: \"10.0.0.1/33\"");

        assert!(result.is_err());
    }
}
