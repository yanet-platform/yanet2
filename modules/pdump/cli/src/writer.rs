use pcap_file::pcap::{PcapHeader, PcapPacket};
use pcap_file::pcapng::blocks::Block;
use pcap_file::pcapng::blocks::enhanced_packet::EnhancedPacketBlock;
use pcap_file::pcapng::blocks::interface_description::{
    InterfaceDescriptionBlock, InterfaceDescriptionOption, TsResolution,
};
use pcap_file::{DataLink, Endianness};
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
use crate::printer;

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
    pretty: bool,
    base_ts: Option<u64>,
}

struct Pcap {
    inner: PcapWriter<PdumpOutput>,
}

struct PcapNg {
    inner: PcapNgWriter<PdumpOutput>,
    interface_id: u32,
}

enum PdumpWriter {
    Text(Text),
    Pcap(Pcap),
    PcapNg(PcapNg),
}

impl PdumpWriter {
    fn new(fmt: DumpOutputFormat, dst: &str, snaplen: u32) -> Result<Self, Box<dyn Error>> {
        let output = PdumpOutput::new(dst)?;

        let writer = match fmt {
            DumpOutputFormat::Text => PdumpWriter::Text(Text {
                inner: output,
                pretty: false,
                base_ts: None,
            }),
            DumpOutputFormat::Pretty => PdumpWriter::Text(Text {
                inner: output,
                pretty: true,
                base_ts: None,
            }),
            DumpOutputFormat::Pcap => {
                let header = PcapHeader {
                    snaplen,
                    ts_resolution: pcap_file::TsResolution::NanoSecond,
                    endianness: Endianness::Little,
                    ..Default::default()
                };
                let pcap_writer = PcapWriter::with_header(output, header)?;
                PdumpWriter::Pcap(Pcap { inner: pcap_writer })
            }
            DumpOutputFormat::PcapNg => {
                let mut pcapng_writer = PcapNgWriter::with_endianness(output, Endianness::Little)?;

                // Create and write an Interface Description Block
                let interface_block = InterfaceDescriptionBlock {
                    linktype: DataLink::ETHERNET,
                    snaplen,
                    options: vec![InterfaceDescriptionOption::IfTsResol(TsResolution::NANO.to_raw())],
                };

                // Write the interface description block
                pcapng_writer.write_block(&Block::InterfaceDescription(interface_block))?;

                // Interface ID is 0 for the first (and only) interface
                let interface_id = 0u32;

                PdumpWriter::PcapNg(PcapNg { inner: pcapng_writer, interface_id })
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
        let mut meta = rec.meta.unwrap();

        let ts = match &writer.base_ts {
            None => {
                // Store the timestamp of the first packet in the writer to establish a baseline.
                writer.base_ts = Some(meta.timestamp);
                0
            }
            // Align timestamps relative to the first packet.
            Some(v) => meta.timestamp - *v,
        };
        meta.timestamp = ts;

        if writer.pretty {
            printer::pretty_print_metadata(&mut writer.inner, &meta)?;
            printer::pretty_print_ethernet_frame(&mut writer.inner, &rec.data, meta.packet_len)?;
        } else {
            printer::pretty_print_metadata_concise(&mut writer.inner, &meta)?;
            printer::pretty_print_ethernet_frame_concise(&mut writer.inner, &rec.data, meta.packet_len)?;
        }
        Ok(0)
    }

    fn write_pcap(writer: &mut Pcap, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let meta = rec.meta.unwrap();
        let ts = Duration::from_nanos(meta.timestamp);
        let packet = PcapPacket::new_owned(ts, meta.packet_len, rec.data);
        Ok(writer.inner.write_packet(&packet)?)
    }

    fn write_pcapng(writer: &mut PcapNg, rec: pdumppb::Record) -> Result<usize, Box<dyn Error>> {
        let meta = rec.meta.unwrap();
        let ts = Duration::from_nanos(meta.timestamp);

        let mut packet_block = EnhancedPacketBlock::default();
        packet_block.interface_id = writer.interface_id;
        packet_block.timestamp = ts;
        packet_block.original_len = meta.packet_len;
        packet_block.data = rec.data.into();
        packet_block.set_write_ts_resolution(TsResolution::NANO);

        Ok(writer.inner.write_block(&Block::EnhancedPacket(packet_block))?)
    }
}

pub fn pdump_write(
    config: Vec<pdumppb::Config>,
    mut rx: mpsc::Receiver<pdumppb::Record>,
    packet_limit: Option<u64>,
    fmt: DumpOutputFormat,
    dst: &str,
) {
    let max_snaplen = config.iter().fold(0, |sl, e| sl.max(e.snaplen));
    let mut writer = match PdumpWriter::new(fmt, dst, max_snaplen) {
        Ok(w) => w,
        Err(e) => {
            log::error!("failed to create pdump writer at '{dst}': {e}");
            return;
        }
    };
    let mut count = 0;
    while let Some(rec) = rx.blocking_recv() {
        if let Err(e) = writer.write(rec) {
            log::error!("failed to write record: {e}");
            break;
        };
        if let Some(limit) = packet_limit {
            if count >= limit {
                log::debug!("stopping writer because the packet capture limit has been reached: {limit}");

                break;
            }
        }
        count += 1;
    }
    _ = writer.flush().map_err(|e| {
        log::error!("failed to flush writer: {e}");
    });
}

pub async fn pdump_stream_reader(
    mut stream: Streaming<pdumppb::Record>,
    tx: mpsc::Sender<pdumppb::Record>,
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
                        log::warn!("error on gRPC stream: {e}");
                        return;
                    }
                    Ok(None) => return,
                    Ok(Some(rec)) => {
                        if let Err(e) = tx.send(rec).await {
                            log::warn!("failed to send Record to pdump writer via mpsc channel: {e}");
                            return;
                        };
                    }
                }
            }
        }
    }
}
