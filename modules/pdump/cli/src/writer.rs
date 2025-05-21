use pcap_file::pcap::PcapPacket;
use pcap_file::{pcap::PcapWriter, pcapng::PcapNgWriter};
use std::error::Error;
use std::time::Duration;
use std::{
    fs,
    io::{self, Write},
};
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;
use tonic::codec::Streaming;

use crate::args::DumpOutputFormat;
use crate::pdumppb;

enum PdumpOutput {
    Stdout(io::Stdout),
    File(fs::File),
}

impl PdumpOutput {
    fn new(dst: &str) -> io::Result<PdumpOutput> {
        match dst {
            "-" | "/dev/stdout" => Ok(PdumpOutput::Stdout(io::stdout())),
            _ => Ok(PdumpOutput::File(fs::File::create(dst)?)),
        }
    }
}

impl io::Write for PdumpOutput {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        match self {
            PdumpOutput::Stdout(stdout) => stdout.write(buf),
            PdumpOutput::File(file) => file.write(buf),
        }
    }

    fn flush(&mut self) -> io::Result<()> {
        match self {
            PdumpOutput::Stdout(stdout) => stdout.flush(),
            PdumpOutput::File(file) => file.flush(),
        }
    }
}

struct Text {
    inner: PdumpOutput,
}

struct Pcap {
    inner: PcapWriter<PdumpOutput>,
}

struct PcapNg {
    inner: PcapNgWriter<PdumpOutput>,
}

enum PdumpWriter {
    Text(Text),
    Pcap(Pcap),
    PcapNg(PcapNg),
}

impl PdumpWriter {
    fn new(fmt: DumpOutputFormat, dst: &str) -> Result<Self, Box<dyn Error>> {
        let output = PdumpOutput::new(dst)?;

        let writer = match fmt {
            DumpOutputFormat::Text => PdumpWriter::Text(Text { inner: output }),
            DumpOutputFormat::Pcap => {
                // FIXME: Construct the pcap header.
                // Issue a get config request before the read request.
                let pcap_writer = PcapWriter::new(output)?;
                PdumpWriter::Pcap(Pcap { inner: pcap_writer })
            }
            DumpOutputFormat::PcapNg => {
                let pcapng_writer = PcapNgWriter::new(output)?;
                PdumpWriter::PcapNg(PcapNg { inner: pcapng_writer })
            }
        };
        Ok(writer)
    }

    fn write(&mut self, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        match self {
            PdumpWriter::Text(writer) => PdumpWriter::write_text(writer, rec),
            PdumpWriter::Pcap(writer) => PdumpWriter::write_pcap(writer, rec),
            PdumpWriter::PcapNg(writer) => PdumpWriter::write_pcapng(writer, rec),
        }
    }

    fn flush(&mut self) -> Result<(), Box<dyn Error>> {
        match self {
            PdumpWriter::Text(writer) => Ok(writer.inner.flush()?),
            PdumpWriter::Pcap(writer) => Ok(writer.inner.flush()?),
            PdumpWriter::PcapNg(writer) => Ok(writer.inner.get_mut().flush()?),
        }
    }

    fn write_text(writer: &mut Text, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        Ok(writer
            .inner
            .write(format!("FIXME Pretty print: {:?}\n", rec.meta).as_bytes())?)
    }

    fn write_pcap(writer: &mut Pcap, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let meta = rec.meta.unwrap();
        let ts = Duration::from_nanos(meta.timestamp);
        let packet = PcapPacket::new_owned(ts, meta.packet_len, rec.data);
        Ok(writer.inner.write_packet(&packet)?)
    }

    fn write_pcapng(_writer: &mut PcapNg, _rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        todo!()
    }
}

pub fn pdump_write(fmt: DumpOutputFormat, mut rx: mpsc::UnboundedReceiver<pdumppb::Record>, dst: &str) {
    let mut writer = match PdumpWriter::new(fmt, dst) {
        Ok(w) => w,
        Err(e) => {
            log::error!("failed to create pdump writer at '{}': {}", dst, e);
            return;
        }
    };
    while let Some(rec) = rx.blocking_recv() {
        if let Err(e) = writer.write(rec) {
            log::error!("failed to write record: {}", e);
            break;
        };
    }
    _ = writer.flush().map_err(|e| {
        log::error!("failed to flush writer: {}", e);
    });
}

pub async fn pdump_stream_reader(
    mut stream: Streaming<pdumppb::Record>,
    tx: mpsc::UnboundedSender<pdumppb::Record>,
    done: CancellationToken,
) {
    loop {
        tokio::select! {
            biased;
            _ = done.cancelled() => {
                return;
            }
            message = stream.message() => {
                match message {
                    Err(e) => {
                        log::warn!("error on gRPC stream: {}", e);
                        return;
                    }
                    Ok(None) => return,
                    Ok(Some(rec)) => {
                        if let Err(e) = tx.send(rec) {
                            log::error!("failed to send record to pdump writer via mpsc channel: {}", e);
                            return;
                        };
                    }
                }
            }
        }
    }
}
