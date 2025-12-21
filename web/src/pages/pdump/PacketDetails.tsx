import React from 'react';
import { Box, Text, Card, Flex } from '@gravity-ui/uikit';
import { formatHexDump, formatTCPFlags } from '../../utils/packetParser';
import type { CapturedPacket } from './types';
import './pdump.css';

interface PacketDetailsProps {
    packet: CapturedPacket;
}

const Section: React.FC<{ title: string; children: React.ReactNode }> = ({ title, children }) => (
    <Box className="packet-section">
        <Text variant="subheader-1" className="packet-section__title">
            {title}
        </Text>
        {children}
    </Box>
);

const Field: React.FC<{ label: string; value: string | number | boolean }> = ({ label, value }) => (
    <Flex gap={2} className="packet-field">
        <Text variant="body-1" color="secondary" className="packet-field__label">
            {label}:
        </Text>
        <Text variant="code-1">
            {typeof value === 'boolean' ? (value ? 'true' : 'false') : value}
        </Text>
    </Flex>
);

export const PacketDetails: React.FC<PacketDetailsProps> = ({ packet }) => {
    const { parsed, record } = packet;

    return (
        <Card theme="normal" className="packet-details-card">
            <Text variant="header-1" className="packet-details-card__title">
                Packet Details
            </Text>

            {/* Metadata */}
            <Section title="Capture Info">
                <Field label="Worker" value={record.meta?.workerIdx ?? 'N/A'} />
                <Field label="Pipeline" value={record.meta?.pipelineIdx ?? 'N/A'} />
                <Field label="RX Device" value={record.meta?.rxDeviceId ?? 'N/A'} />
                <Field label="TX Device" value={record.meta?.txDeviceId ?? 'N/A'} />
                <Field label="Queue" value={record.meta?.queue ?? 'N/A'} />
                <Field label="Packet Length" value={record.meta?.packetLen ?? parsed.raw.length} />
                <Field label="Captured" value={record.meta?.dataSize ?? parsed.raw.length} />
            </Section>

            {/* Ethernet */}
            {parsed.ethernet && (
                <Section title="Ethernet">
                    <Field label="Source MAC" value={parsed.ethernet.srcMac} />
                    <Field label="Dest MAC" value={parsed.ethernet.dstMac} />
                    <Field label="EtherType" value={`${parsed.ethernet.etherTypeName} (0x${parsed.ethernet.etherType.toString(16)})`} />
                </Section>
            )}

            {/* IPv4 */}
            {parsed.ipv4 && (
                <Section title="IPv4">
                    <Field label="Source IP" value={parsed.ipv4.srcAddr} />
                    <Field label="Dest IP" value={parsed.ipv4.dstAddr} />
                    <Field label="Protocol" value={`${parsed.ipv4.protocolName} (${parsed.ipv4.protocol})`} />
                    <Field label="TTL" value={parsed.ipv4.ttl} />
                    <Field label="Total Length" value={parsed.ipv4.totalLength} />
                    <Field label="ID" value={`0x${parsed.ipv4.identification.toString(16)}`} />
                    <Field label="DSCP" value={parsed.ipv4.dscp} />
                    <Field label="ECN" value={parsed.ipv4.ecn} />
                    <Field label="Don't Fragment" value={parsed.ipv4.flags.dontFragment} />
                    <Field label="More Fragments" value={parsed.ipv4.flags.moreFragments} />
                    <Field label="Fragment Offset" value={parsed.ipv4.fragmentOffset} />
                </Section>
            )}

            {/* IPv6 */}
            {parsed.ipv6 && (
                <Section title="IPv6">
                    <Field label="Source IP" value={parsed.ipv6.srcAddr} />
                    <Field label="Dest IP" value={parsed.ipv6.dstAddr} />
                    <Field label="Next Header" value={`${parsed.ipv6.nextHeaderName} (${parsed.ipv6.nextHeader})`} />
                    <Field label="Hop Limit" value={parsed.ipv6.hopLimit} />
                    <Field label="Payload Length" value={parsed.ipv6.payloadLength} />
                    <Field label="Traffic Class" value={parsed.ipv6.trafficClass} />
                    <Field label="Flow Label" value={`0x${parsed.ipv6.flowLabel.toString(16)}`} />
                </Section>
            )}

            {/* TCP */}
            {parsed.tcp && (
                <Section title="TCP">
                    <Field label="Source Port" value={parsed.tcp.srcPort} />
                    <Field label="Dest Port" value={parsed.tcp.dstPort} />
                    <Field label="Sequence" value={parsed.tcp.seqNum} />
                    <Field label="Acknowledgment" value={parsed.tcp.ackNum} />
                    <Field label="Flags" value={formatTCPFlags(parsed.tcp.flags) || 'none'} />
                    <Field label="Window Size" value={parsed.tcp.windowSize} />
                    <Field label="Data Offset" value={`${parsed.tcp.dataOffset * 4} bytes`} />
                    <Field label="Checksum" value={`0x${parsed.tcp.checksum.toString(16)}`} />
                </Section>
            )}

            {/* UDP */}
            {parsed.udp && (
                <Section title="UDP">
                    <Field label="Source Port" value={parsed.udp.srcPort} />
                    <Field label="Dest Port" value={parsed.udp.dstPort} />
                    <Field label="Length" value={parsed.udp.length} />
                    <Field label="Checksum" value={`0x${parsed.udp.checksum.toString(16)}`} />
                </Section>
            )}

            {/* ICMP */}
            {parsed.icmp && (
                <Section title="ICMP">
                    <Field label="Type" value={`${parsed.icmp.typeName} (${parsed.icmp.type})`} />
                    <Field label="Code" value={parsed.icmp.code} />
                    <Field label="Checksum" value={`0x${parsed.icmp.checksum.toString(16)}`} />
                </Section>
            )}

            <Box className="packet-details-card__spacer" />

            {/* Hex Dump */}
            <Section title="Hex Dump">
                <Box className="packet-hexdump">
                    <pre className="packet-hexdump__pre">
                        {formatHexDump(parsed.raw)}
                    </pre>
                </Box>
            </Section>
        </Card>
    );
};
