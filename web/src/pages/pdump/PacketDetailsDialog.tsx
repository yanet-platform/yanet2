import React from 'react';
import { Dialog, Box, Text, Flex } from '@gravity-ui/uikit';
import { formatHexDump, formatTCPFlags } from '../../utils/packetParser';
import type { CapturedPacket } from './types';
import './pdump.css';

interface PacketDetailsDialogProps {
    packet: CapturedPacket | null;
    open: boolean;
    onClose: () => void;
}

const Section: React.FC<{ title: string; children: React.ReactNode }> = ({ title, children }) => (
    <Box className="packet-dialog-section">
        <Text variant="subheader-1" className="packet-dialog-section__title">
            {title}
        </Text>
        {children}
    </Box>
);

const Field: React.FC<{ label: string; value: string | number | boolean }> = ({ label, value }) => (
    <Flex gap={2} className="packet-field--compact">
        <Text variant="body-1" color="secondary" className="packet-field__label--compact">
            {label}:
        </Text>
        <Text variant="code-1" className="packet-field__value--compact">
            {typeof value === 'boolean' ? (value ? 'true' : 'false') : value}
        </Text>
    </Flex>
);

export const PacketDetailsDialog: React.FC<PacketDetailsDialogProps> = ({
    packet,
    open,
    onClose,
}) => {
    if (!packet) return null;

    const { parsed, record, timestamp } = packet;

    const formatTime = (date: Date): string => {
        return date.toLocaleTimeString('en-US', {
            hour12: false,
            hour: '2-digit',
            minute: '2-digit',
            second: '2-digit',
            fractionalSecondDigits: 3,
        } as Intl.DateTimeFormatOptions);
    };

    return (
        <Dialog
            open={open}
            onClose={onClose}
            size="m"
        >
            <Dialog.Header caption={`Packet #${packet.id} - ${formatTime(timestamp)}`} />
            <Dialog.Body>
                <Box className="packet-dialog__content">
                    {/* Capture Info */}
                    <Section title="Capture Info">
                        <Flex gap={4} className="packet-dialog__flex-wrap">
                            <Field label="Worker" value={record.meta?.workerIdx ?? 'N/A'} />
                            <Field label="Pipeline" value={record.meta?.pipelineIdx ?? 'N/A'} />
                            <Field label="RX Device" value={record.meta?.rxDeviceId ?? 'N/A'} />
                            <Field label="TX Device" value={record.meta?.txDeviceId ?? 'N/A'} />
                            <Field label="Queue" value={record.meta?.queue ?? 'N/A'} />
                            <Field label="Length" value={record.meta?.packetLen ?? parsed.raw.length} />
                            <Field label="Captured" value={record.meta?.dataSize ?? parsed.raw.length} />
                        </Flex>
                    </Section>

                    {/* Ethernet Layer */}
                    {parsed.ethernet && (
                        <Section title="Ethernet">
                            <Field label="Source MAC" value={parsed.ethernet.srcMac} />
                            <Field label="Dest MAC" value={parsed.ethernet.dstMac} />
                            <Field label="EtherType" value={`${parsed.ethernet.etherTypeName} (0x${parsed.ethernet.etherType.toString(16)})`} />
                        </Section>
                    )}

                    {/* IPv4 Layer */}
                    {parsed.ipv4 && (
                        <Section title="IPv4">
                            <Flex gap={6}>
                                <Box>
                                    <Field label="Source IP" value={parsed.ipv4.srcAddr} />
                                    <Field label="Dest IP" value={parsed.ipv4.dstAddr} />
                                    <Field label="Protocol" value={`${parsed.ipv4.protocolName} (${parsed.ipv4.protocol})`} />
                                    <Field label="TTL" value={parsed.ipv4.ttl} />
                                </Box>
                                <Box>
                                    <Field label="Total Length" value={parsed.ipv4.totalLength} />
                                    <Field label="ID" value={`0x${parsed.ipv4.identification.toString(16)}`} />
                                    <Field label="DSCP/ECN" value={`${parsed.ipv4.dscp}/${parsed.ipv4.ecn}`} />
                                    <Field label="Flags" value={`DF=${parsed.ipv4.flags.dontFragment ? 1 : 0} MF=${parsed.ipv4.flags.moreFragments ? 1 : 0}`} />
                                    <Field label="Frag Offset" value={parsed.ipv4.fragmentOffset} />
                                </Box>
                            </Flex>
                        </Section>
                    )}

                    {/* IPv6 Layer */}
                    {parsed.ipv6 && (
                        <Section title="IPv6">
                            <Field label="Source IP" value={parsed.ipv6.srcAddr} />
                            <Field label="Dest IP" value={parsed.ipv6.dstAddr} />
                            <Flex gap={6} className="packet-dialog__margin-top">
                                <Box>
                                    <Field label="Next Header" value={`${parsed.ipv6.nextHeaderName} (${parsed.ipv6.nextHeader})`} />
                                    <Field label="Hop Limit" value={parsed.ipv6.hopLimit} />
                                </Box>
                                <Box>
                                    <Field label="Payload Len" value={parsed.ipv6.payloadLength} />
                                    <Field label="Traffic Class" value={parsed.ipv6.trafficClass} />
                                    <Field label="Flow Label" value={`0x${parsed.ipv6.flowLabel.toString(16)}`} />
                                </Box>
                            </Flex>
                        </Section>
                    )}

                    {/* TCP Layer */}
                    {parsed.tcp && (
                        <Section title="TCP">
                            <Flex gap={6}>
                                <Box>
                                    <Field label="Source Port" value={parsed.tcp.srcPort} />
                                    <Field label="Dest Port" value={parsed.tcp.dstPort} />
                                    <Field label="Sequence" value={parsed.tcp.seqNum} />
                                    <Field label="Ack" value={parsed.tcp.ackNum} />
                                </Box>
                                <Box>
                                    <Field label="Flags" value={formatTCPFlags(parsed.tcp.flags) || 'none'} />
                                    <Field label="Window" value={parsed.tcp.windowSize} />
                                    <Field label="Data Offset" value={`${parsed.tcp.dataOffset * 4} bytes`} />
                                    <Field label="Checksum" value={`0x${parsed.tcp.checksum.toString(16)}`} />
                                </Box>
                            </Flex>
                        </Section>
                    )}

                    {/* UDP Layer */}
                    {parsed.udp && (
                        <Section title="UDP">
                            <Flex gap={6}>
                                <Box>
                                    <Field label="Source Port" value={parsed.udp.srcPort} />
                                    <Field label="Dest Port" value={parsed.udp.dstPort} />
                                </Box>
                                <Box>
                                    <Field label="Length" value={parsed.udp.length} />
                                    <Field label="Checksum" value={`0x${parsed.udp.checksum.toString(16)}`} />
                                </Box>
                            </Flex>
                        </Section>
                    )}

                    {/* ICMP Layer */}
                    {parsed.icmp && (
                        <Section title="ICMP">
                            <Field label="Type" value={`${parsed.icmp.typeName} (${parsed.icmp.type})`} />
                            <Field label="Code" value={parsed.icmp.code} />
                            <Field label="Checksum" value={`0x${parsed.icmp.checksum.toString(16)}`} />
                        </Section>
                    )}

                    {/* Hex Dump */}
                    <Section title="Hex Dump">
                        <Box className="packet-hexdump--compact">
                            <pre className="packet-hexdump__pre">
                                {formatHexDump(parsed.raw)}
                            </pre>
                        </Box>
                    </Section>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                textButtonCancel="Close"
            />
        </Dialog>
    );
};
