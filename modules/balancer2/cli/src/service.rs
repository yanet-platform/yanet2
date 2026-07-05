use std::{net::IpAddr, time};

use ptree::TreeBuilder;
use tonic::codec::CompressionEncoding;
use yanet_cli_balancer2::balancerpb::{
    self, GetConfigRequest, GetMetricsRequest, GetStateRequest, ListConfigsRequest, ListSessionsRequest,
    ListSessionsStatesRequest, PacketHandlerRef, RealUpdate, UpdateConfigRequest, UpdateRealsRequest,
    UpdateSessionsStateRequest, balancer_client::BalancerClient,
};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

use crate::{
    ConfigCmd, MetricsCmd, ModeCmd, ShowCmd, UpdateCmd, VsId,
    config::{BalancerConfig, ConfigParts},
    display, ip_to_bytes,
    reals::{DisableRealCmd, EnableRealCmd, RealsMode},
    sessions::{SessionsMode, SessionsShowCmd, SessionsUpdateCmd},
};

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.balancer2.controlplane.balancerpb.v1.Balancer";

pub struct Balancer2Service {
    service: Service<BalancerClient<LayeredChannel>>,
}

impl Balancer2Service {
    pub async fn connect(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            BalancerClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn handle(&mut self, mode: ModeCmd, format: CommonFormat) -> Result<(), Error> {
        match mode {
            ModeCmd::Update(cmd) => self.update(cmd).await,
            ModeCmd::List => self.list().await,
            ModeCmd::Config(cmd) => self.config(cmd).await,
            ModeCmd::Show(cmd) => self.show(cmd).await,
            ModeCmd::Sessions(cmd) => match cmd.mode {
                SessionsMode::List => self.sessions_list().await,
                SessionsMode::Show(cmd) => self.sessions_show(cmd, format).await,
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
        let yaml_config = BalancerConfig::from_yaml_file(&cmd.config)
            .map_err(|err| self.service.invalid("update", err.to_string()))?;
        let parts: ConfigParts = yaml_config
            .try_into()
            .map_err(|err: Box<dyn std::error::Error>| self.service.invalid("update", err.to_string()))?;

        let request = UpdateConfigRequest {
            config_name: cmd.name.clone(),
            sessions_state_name: cmd.sessions,
            vs: parts.vs,
            timeouts: parts.timeouts,
            addr: parts.addr,
            wlc: parts.wlc,
        };
        log::trace!("update config request: {request:?}");

        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated balancer {}.", cmd.name));

        Ok(())
    }

    async fn list(&mut self) -> Result<(), Error> {
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
            &response.names,
            response.names.is_empty(),
            format_args!("no balancer configs"),
            || {
                let mut tree = TreeBuilder::new("Balancers".to_owned());
                for name in &response.names {
                    tree.add_empty_child(name.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Error> {
        let request = GetConfigRequest { config_name: cmd.name };
        log::trace!("get config request: {request:?}");

        let response = self
            .service
            .client()
            .get_config(request)
            .await
            .map_err(self.service.status("config"))?
            .into_inner();
        log::debug!("get config response: {response:?}");

        output::data(&response, false, format_args!(""), || {
            let mut json_value =
                serde_json::to_value(&response).expect("balancer config JSON conversion must not fail");
            display::prettify_json(&mut json_value);
            let yaml = serde_yaml::to_string(&json_value).expect("balancer config YAML serialization must not fail");
            print!("{yaml}");
        });

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

        let filter = cmd.filter.to_proto();
        let request = GetStateRequest {
            config_name: cmd.name,
            packet_handler_ref,
            filter,
        };
        log::trace!("get state request: {request:?}");

        let response = self
            .service
            .client()
            .get_state(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
        log::debug!("get state response: {response:?}");

        output::data(
            &response.states,
            response.states.is_empty(),
            format_args!("no balancer state found"),
            || display::print_table_view(&response.states, &opts),
        );

        Ok(())
    }

    async fn sessions_list(&mut self) -> Result<(), Error> {
        let request = ListSessionsStatesRequest {};
        log::trace!("list sessions states request: {request:?}");

        let response = self
            .service
            .client()
            .list_sessions_states(request)
            .await
            .map_err(self.service.status("sessions list"))?
            .into_inner();
        log::debug!("list sessions states response: {response:?}");

        output::data(
            &response.names,
            response.names.is_empty(),
            format_args!("no sessions states"),
            || {
                let mut tree = TreeBuilder::new("Sessions States".to_owned());
                for name in &response.names {
                    tree.add_empty_child(name.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    async fn sessions_show(&mut self, cmd: SessionsShowCmd, format: CommonFormat) -> Result<(), Error> {
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

        if format == CommonFormat::Human {
            display::print_sessions_header();
        }

        let now = time::SystemTime::now()
            .duration_since(time::UNIX_EPOCH)
            .expect("system clock before UNIX epoch")
            .as_secs() as i64;
        let mut printed = 0usize;

        while let Some(session) = stream.message().await.map_err(self.service.status("sessions show"))? {
            match format {
                CommonFormat::Human => display::print_session(&session, now),
                CommonFormat::Json => println!(
                    "{}",
                    serde_json::to_string(&session).expect("balancer session JSON serialization must not fail")
                ),
            }
            printed += 1;
        }

        if printed == 0 && format == CommonFormat::Human {
            eprintln!("no sessions");
        }

        Ok(())
    }

    async fn sessions_update(&mut self, cmd: SessionsUpdateCmd) -> Result<(), Error> {
        let request = UpdateSessionsStateRequest {
            sessions_state_name: cmd.name.clone(),
            capacity: cmd.capacity,
        };
        log::trace!("update sessions state request: {request:?}");

        self.service
            .client()
            .update_sessions_state(request)
            .await
            .map_err(self.service.status("sessions update"))?;

        output::success(
            "sessions update",
            format_args!("Updated sessions state {} (capacity: {}).", cmd.name, cmd.capacity),
        );

        Ok(())
    }

    async fn metrics(&mut self, _cmd: MetricsCmd) -> Result<(), Error> {
        let request = GetMetricsRequest {};
        log::trace!("get metrics request: {request:?}");

        let response = self
            .service
            .client()
            .get_metrics(request)
            .await
            .map_err(self.service.status("metrics"))?
            .into_inner();
        log::debug!("get metrics response: {response:?}");

        output::data(&response, false, format_args!(""), || {
            let mut json_value =
                serde_json::to_value(&response).expect("balancer metrics JSON conversion must not fail");
            display::prettify_json(&mut json_value);
            let json = serde_json::to_string(&json_value).expect("balancer metrics JSON serialization must not fail");
            println!("{json}");
        });

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
        log::trace!("update reals request: {request:?}");

        self.service
            .client()
            .update_reals(request)
            .await
            .map_err(self.service.status(action))?;

        output::success(action, format_args!("Updated reals for balancer {config_name}."));

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
