use core::error::Error;

use log::LevelFilter;
use simple_logger::SimpleLogger;

pub fn init(verbosity: usize) -> Result<(), Box<dyn Error>> {
    let level = match verbosity {
        0 => LevelFilter::Info,
        1 => LevelFilter::Debug,
        _ => LevelFilter::Trace,
    };

    SimpleLogger::new()
        .with_module_level("ync", level)
        .with_utc_timestamps()
        .init()?;

    Ok(())
}
