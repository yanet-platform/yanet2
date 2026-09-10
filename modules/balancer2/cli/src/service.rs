use core::net::IpAddr;
use std::time;

use commonpb::pb::GetMetricsRequest;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    display::print_names_with_hint,
    errors::Error,
    output, yaml,
};

use crate::{
    ConfigCmd, MetricsCmd, ModeCmd, ShowCmd, UpdateCmd, VsId,
    balancerpb::{
        self, GetConfigRequest, GetStateRequest, ListConfigsRequest, ListSessionsRequest, ListSessionsStatesRequest,
        PacketHandlerRef, RealUpdate, UpdateConfigRequest, UpdateRealsRequest, UpdateSessionsStateRequest,
        balancer_client::BalancerClient,
    },
    config::{BalancerConfig, ConfigParts},
    display, ip_to_bytes,
    reals::{DisableRealCmd, EnableRealCmd, RealsMode},
    sessions::{SessionsMode, SessionsShowCmd, SessionsUpdateCmd},
};

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.balancer2.controlplane.balancerpb.v1.Balancer";

pub fn client(channel: LayeredChannel) -> BalancerClient<LayeredChannel> {
    BalancerClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

pub struct Balancer2Service {
    service: Service<BalancerClient<LayeredChannel>>,
}

impl Balancer2Service {
    pub async fn connect(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, client).await?;

        Ok(Self { service })
    }

    pub async fn handle(&mut self, mode: ModeCmd) -> Result<(), Error> {
        match mode {
            ModeCmd::Update(cmd) => self.update(cmd).await,
            ModeCmd::List => self.list().await,
            ModeCmd::Config(cmd) => self.config(cmd).await,
            ModeCmd::Show(cmd) => self.show(cmd).await,
            ModeCmd::Sessions(cmd) => match cmd.mode {
                SessionsMode::List => self.sessions_list().await,
                SessionsMode::Show(cmd) => self.sessions_show(cmd).await,
                SessionsMode::Update(cmd) => self.sessions_update(cmd).await,
            },
            ModeCmd::Metrics(cmd) => self.metrics(cmd).await,
            ModeCmd::Reals(cmd) => match cmd.mode {
                RealsMode::Enable(cmd) => self.enable_real(cmd).await,
                RealsMode::Disable(cmd) => self.disable_real(cmd).await,
            },
        }
    }

    async fn update(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let yaml_config: BalancerConfig =
            yaml::load(&cmd.file).map_err(|err| self.service.invalid("update", err.to_string()))?;
        let parts: ConfigParts = yaml_config
            .try_into()
            .map_err(|err: Box<dyn core::error::Error>| self.service.invalid("update", err.to_string()))?;

        let request = UpdateConfigRequest {
            config_name: cmd.name.clone(),
            sessions_state_name: cmd.sessions,
            vs: parts.vs,
            timeouts: parts.timeouts,
            addr: parts.addr,
            wlc: parts.wlc,
        };
        self.service
            .unary("update", request, async |client, request| {
                client.update_config(request).await
            })
            .await?;

        output::success("update", format_args!("Updated config '{}'.", cmd.name));

        Ok(())
    }

    async fn list(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .unary("list", ListConfigsRequest {}, async |client, request| {
                client.list_configs(request).await
            })
            .await?;

        output::data(
            || &response.names,
            || {
                print_names_with_hint(
                    &response.names,
                    format_args!("No balancer configurations found."),
                    format_args!(
                        "create one with 'yanet-cli-balancer2 update --name <name> --sessions <sessions-name> <path>'"
                    ),
                )
            },
        );

        Ok(())
    }

    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Error> {
        let request = GetConfigRequest { config_name: cmd.name.clone() };
        let response = self
            .service
            .unary_with(
                "config",
                request,
                self.service.not_found("config", &format!("config '{}'", cmd.name)),
                async |client, request| client.get_config(request).await,
            )
            .await?;

        output::data(
            || &response,
            || {
                let mut json_value =
                    serde_json::to_value(&response).expect("balancer config JSON conversion must not fail");
                display::prettify_json(&mut json_value);
                let yaml =
                    serde_yaml::to_string(&json_value).expect("balancer config YAML serialization must not fail");
                print!("{yaml}");
            },
        );

        Ok(())
    }

    async fn show(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let opts = display::ShowOptions {
            stats: cmd.stats || cmd.detail,
            acl: cmd.acl || cmd.detail,
            peers: cmd.peers || cmd.detail,
            decap: cmd.decap || cmd.detail,
        };

        let packet_handler_ref =
            if cmd.device.is_some() || cmd.pipeline.is_some() || cmd.function.is_some() || cmd.chain.is_some() {
                Some(PacketHandlerRef {
                    device: cmd.device,
                    pipeline: cmd.pipeline,
                    function: cmd.function,
                    chain: cmd.chain,
                })
            } else {
                None
            };

        let name = cmd.name.clone();
        let filter = cmd.filter.to_proto();
        let request = GetStateRequest {
            config_name: cmd.name,
            packet_handler_ref,
            filter,
        };
        let response = self
            .service
            .unary("show", request, async |client, request| client.get_state(request).await)
            .await?;

        output::data(
            || &response.states,
            || {
                if response.states.is_empty() {
                    output::empty(format_args!("No balancer state found for '{name}'."));
                    return;
                }

                display::print_table_view(&response.states, &opts);
            },
        );

        Ok(())
    }

    async fn sessions_list(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .unary(
                "sessions list",
                ListSessionsStatesRequest {},
                async |client, request| client.list_sessions_states(request).await,
            )
            .await?;

        output::data(
            || &response.names,
            || {
                print_names_with_hint(
                    &response.names,
                    format_args!("No session states found."),
                    format_args!("create one with 'yanet-cli-balancer2 sessions update --name <name> --capacity <n>'"),
                )
            },
        );

        Ok(())
    }

    async fn sessions_show(&mut self, cmd: SessionsShowCmd) -> Result<(), Error> {
        let name = cmd.name.clone();
        let request = ListSessionsRequest {
            sessions_state_name: cmd.name,
            filter: cmd.filter.to_proto(),
        };
        log::trace!("list sessions request: {request:?}");

        let mut stream = self
            .service
            .client()
            .list_sessions(request)
            .await
            .map_err(self.service.status("sessions show"))?
            .into_inner();

        let now = time::SystemTime::now()
            .duration_since(time::UNIX_EPOCH)
            .expect("system clock before UNIX epoch")
            .as_secs() as i64;
        let mut rows = output::rows(display::print_sessions_header, |session| {
            display::print_session(session, now)
        });

        while let Some(session) = stream.message().await.map_err(self.service.status("sessions show"))? {
            rows.push(&session);
        }

        rows.finish(format_args!("No sessions found for '{name}'."));

        Ok(())
    }

    async fn sessions_update(&mut self, cmd: SessionsUpdateCmd) -> Result<(), Error> {
        let request = UpdateSessionsStateRequest {
            sessions_state_name: cmd.name.clone(),
            capacity: cmd.capacity,
        };
        self.service
            .unary("sessions update", request, async |client, request| {
                client.update_sessions_state(request).await
            })
            .await?;

        output::success(
            "sessions update",
            format_args!("Updated sessions state '{}' (capacity: {}).", cmd.name, cmd.capacity),
        );

        Ok(())
    }

    async fn metrics(&mut self, _cmd: MetricsCmd) -> Result<(), Error> {
        let response = self
            .service
            .unary("metrics", GetMetricsRequest::default(), async |client, request| {
                client.get_metrics(request).await
            })
            .await?;

        output::data(
            || &response,
            || {
                let mut json_value =
                    serde_json::to_value(&response).expect("balancer metrics JSON conversion must not fail");
                display::prettify_json(&mut json_value);
                let json =
                    serde_json::to_string(&json_value).expect("balancer metrics JSON serialization must not fail");
                println!("{json}");

                if response.metrics.is_empty() {
                    output::empty(format_args!("No balancer metrics found."));
                }
            },
        );

        Ok(())
    }

    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Error> {
        let updates = build_real_updates(&cmd.vs, &cmd.reals, Some(true), cmd.weight);
        self.send_real_updates("enable", cmd.name, updates).await
    }

    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Error> {
        let updates = build_real_updates(&cmd.vs, &cmd.reals, Some(false), None);
        self.send_real_updates("disable", cmd.name, updates).await
    }

    async fn send_real_updates(
        &mut self,
        action: &'static str,
        config_name: String,
        updates: Vec<RealUpdate>,
    ) -> Result<(), Error> {
        let request = UpdateRealsRequest {
            config_name: config_name.clone(),
            updates,
        };
        self.service
            .unary(action, request, async |client, request| {
                client.update_reals(request).await
            })
            .await?;

        output::success(action, format_args!("Updated reals of config '{config_name}'."));

        Ok(())
    }
}

fn build_real_updates(vs: &VsId, reals: &[IpAddr], enable: Option<bool>, weight: Option<u32>) -> Vec<RealUpdate> {
    let vs_id: balancerpb::VsIdentifier = vs.into();

    reals
        .iter()
        .map(|real_ip| RealUpdate {
            real_id: Some(balancerpb::RealIdentifier {
                vs: Some(vs_id.clone()),
                real: Some(balancerpb::RelativeRealIdentifier { ip: ip_to_bytes(*real_ip), port: 0 }),
            }),
            enable,
            weight,
        })
        .collect()
}
