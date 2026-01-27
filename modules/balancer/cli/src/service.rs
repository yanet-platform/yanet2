//! gRPC service client implementation

use std::error::Error;

use tonic::{codec::CompressionEncoding, transport::Channel};

use crate::{
    cmd::*,
    entities::BalancerConfig,
    output,
    rpc::{BalancerServiceClient, balancerpb},
};

////////////////////////////////////////////////////////////////////////////////
// Logging macros with custom target
////////////////////////////////////////////////////////////////////////////////

macro_rules! info {
    ($($arg:tt)*) => {
        log::info!(target: "yanet_cli_balancer", $($arg)*)
    };
}

////////////////////////////////////////////////////////////////////////////////
// Service
////////////////////////////////////////////////////////////////////////////////

pub struct BalancerService {
    client: BalancerServiceClient<Channel>,
}

impl BalancerService {
    /// Connect to the gRPC endpoint
    pub async fn connect(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = BalancerServiceClient::connect(endpoint).await?;
        let client = client
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    /// Handle the command
    pub async fn handle_cmd(&mut self, mode: Mode) -> Result<(), Box<dyn Error>> {
        log::trace!("Handling command: {:?}", mode);

        match mode {
            Mode::Update(cmd) => self.update_config(cmd).await,
            Mode::Reals(cmd) => self.handle_reals(cmd).await,
            Mode::Config(cmd) => self.config(cmd).await,
            Mode::List(cmd) => self.list(cmd).await,
            Mode::Stats(cmd) => self.stats(cmd).await,
            Mode::Info(cmd) => self.info(cmd).await,
            Mode::Sessions(cmd) => self.sessions(cmd).await,
            Mode::Graph(cmd) => self.graph(cmd).await,
        }
    }

    /// Update balancer configuration
    async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Box<dyn Error>> {
        info!("Loading configuration from: {}", cmd.config);

        let config = BalancerConfig::from_yaml_file(&cmd.config)?;
        let balancer_config: balancerpb::BalancerConfig = config.try_into()?;

        let request = balancerpb::UpdateConfigRequest {
            name: cmd.name.clone(),
            config: Some(balancer_config),
        };

        log::debug!("Sending UpdateConfig request");
        self.client.update_config(request).await?;

        info!("Successfully updated configuration for '{}'", cmd.name);
        Ok(())
    }

    /// Handle reals commands
    async fn handle_reals(&mut self, cmd: RealsCmd) -> Result<(), Box<dyn Error>> {
        match cmd.mode {
            RealsMode::Enable(cmd) => self.enable_real(cmd).await,
            RealsMode::Disable(cmd) => self.disable_real(cmd).await,
            RealsMode::Flush(cmd) => self.flush_real_updates(cmd).await,
        }
    }

    /// Enable a real server
    async fn enable_real(&mut self, cmd: EnableRealCmd) -> Result<(), Box<dyn Error>> {
        let flush = cmd.flush;
        let name = cmd.name.clone();

        let name_display = name.as_deref().unwrap_or("<auto>");
        info!(
            "Enabling {} real(s) of VS {} for '{}'",
            cmd.reals.len(),
            cmd.vs,
            name_display
        );

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;

        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;

        info!("Successfully buffered real enable");

        // If flush flag is set, immediately flush the updates
        if flush {
            let name_display = name.as_deref().unwrap_or("<auto>");
            info!("Flushing buffered real updates for '{}'", name_display);
            let flush_request = balancerpb::FlushRealUpdatesRequest { name };
            let response = self.client.flush_real_updates(flush_request).await?.into_inner();
            info!("Successfully flushed {} update(s)", response.updates_flushed);
        }

        Ok(())
    }

    /// Disable a real server
    async fn disable_real(&mut self, cmd: DisableRealCmd) -> Result<(), Box<dyn Error>> {
        let flush = cmd.flush;
        let name = cmd.name.clone();
        let reals_count = cmd.reals.len();

        let name_display = name.as_deref().unwrap_or("<auto>");
        info!(
            "Disabling {} real(s) of VS {} for '{}'",
            reals_count, cmd.vs, name_display
        );

        let request: balancerpb::UpdateRealsRequest = cmd.try_into()?;

        log::debug!("Sending UpdateReals request");
        self.client.update_reals(request).await?;

        info!("Successfully buffered real disable");

        // If flush flag is set, immediately flush the updates
        if flush {
            info!("Flushing buffered real updates");
            let flush_request = balancerpb::FlushRealUpdatesRequest { name };
            let response = self.client.flush_real_updates(flush_request).await?.into_inner();
            info!("Successfully flushed {} update(s)", response.updates_flushed);
        }

        Ok(())
    }

    /// Flush buffered real updates
    async fn flush_real_updates(&mut self, cmd: FlushRealUpdatesCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        info!("Flushing buffered real updates for '{}'", name_display);

        let request: balancerpb::FlushRealUpdatesRequest = cmd.into();

        log::debug!("Sending FlushRealUpdates request");
        let response = self.client.flush_real_updates(request).await?.into_inner();

        info!("Successfully flushed {} update(s)", response.updates_flushed);
        Ok(())
    }

    /// Show balancer configuration
    async fn config(&mut self, cmd: ConfigCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching configuration for '{}'", name_display);

        let request: balancerpb::ShowConfigRequest = (&cmd).into();
        let response = self.client.show_config(request).await?.into_inner();

        output::print_show_config(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// List all balancer configurations
    async fn list(&mut self, cmd: ListCmd) -> Result<(), Box<dyn Error>> {
        log::debug!("Fetching all configurations");

        let request = balancerpb::ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();

        output::print_list_configs(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show configuration statistics
    async fn stats(&mut self, cmd: StatsCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching statistics for '{}'", name_display);

        let request: balancerpb::ShowStatsRequest = (&cmd).into();
        let response = self.client.show_stats(request).await?.into_inner();

        output::print_show_stats(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show state information
    async fn info(&mut self, cmd: InfoCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching state info for '{}'", name_display);

        let request: balancerpb::ShowInfoRequest = (&cmd).into();
        let response = self.client.show_info(request).await?.into_inner();

        output::print_show_info(&response, cmd.format.to_format())?;
        Ok(())
    }

    /// Show sessions information
    async fn sessions(&mut self, cmd: SessionsCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching sessions info for '{}'", name_display);

        let request: balancerpb::ShowSessionsRequest = (&cmd).into();
        let response = self.client.show_sessions(request).await?.into_inner();

        output::print_show_sessions(&response, cmd.format.to_format())?;
        Ok(())
    }

    async fn graph(&mut self, cmd: GraphCmd) -> Result<(), Box<dyn Error>> {
        let name_display = cmd.name.as_deref().unwrap_or("<auto>");
        log::debug!("Fetching graph info for '{}'", name_display);

        let request: balancerpb::ShowGraphRequest = (&cmd).into();
        let response = self.client.show_graph(request).await?.into_inner();

        output::print_show_graph(&response, cmd.format.to_format())?;
        Ok(())
    }
}
