import React from 'react';
import { formatTCPFlags } from '../../utils/packetParser';
import { cellStyles, TOTAL_WIDTH, ROW_HEIGHT } from './constants';
import type { CapturedPacket } from './types';

export interface PacketTableRowProps {
    packet: CapturedPacket;
    index: number;
    start: number;
    isSelected: boolean;
    onClick: () => void;
}

const formatTime = (date: Date): string => {
    const pad = (n: number, len: number = 2) => n.toString().padStart(len, '0');
    return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
};

export const PacketTableRow: React.FC<PacketTableRowProps> = ({
    packet,
    index,
    start,
    isSelected,
    onClick,
}) => {
    const { parsed } = packet;

    // Extract source and destination
    let src = '';
    let dst = '';
    let protocol = '';
    let isIPv6 = false;

    if (parsed.ipv4) {
        src = parsed.ipv4.srcAddr;
        dst = parsed.ipv4.dstAddr;
        protocol = parsed.ipv4.protocolName;
    } else if (parsed.ipv6) {
        // Wrap IPv6 addresses in brackets
        src = `[${parsed.ipv6.srcAddr}]`;
        dst = `[${parsed.ipv6.dstAddr}]`;
        protocol = parsed.ipv6.nextHeaderName;
        isIPv6 = true;
    } else if (parsed.ethernet) {
        src = parsed.ethernet.srcMac;
        dst = parsed.ethernet.dstMac;
        protocol = parsed.ethernet.etherTypeName;
    }

    // Add ports for TCP/UDP
    if (parsed.tcp) {
        if (isIPv6) {
            // IPv6 with port: [addr]:port
            src = `${src}:${parsed.tcp.srcPort}`;
            dst = `${dst}:${parsed.tcp.dstPort}`;
        } else {
            src = `${src}:${parsed.tcp.srcPort}`;
            dst = `${dst}:${parsed.tcp.dstPort}`;
        }
        protocol = 'TCP';
    } else if (parsed.udp) {
        if (isIPv6) {
            src = `${src}:${parsed.udp.srcPort}`;
            dst = `${dst}:${parsed.udp.dstPort}`;
        } else {
            src = `${src}:${parsed.udp.srcPort}`;
            dst = `${dst}:${parsed.udp.dstPort}`;
        }
        protocol = 'UDP';
    } else if (parsed.icmp) {
        protocol = 'ICMP';
    }

    // Info column
    let info = '';
    if (parsed.tcp) {
        const flags = formatTCPFlags(parsed.tcp.flags);
        info = `${flags} Seq=${parsed.tcp.seqNum}`;
        if (parsed.tcp.flags.ack) {
            info += ` Ack=${parsed.tcp.ackNum}`;
        }
        info += ` Win=${parsed.tcp.windowSize}`;
    } else if (parsed.udp) {
        info = `Len=${parsed.udp.length}`;
    } else if (parsed.icmp) {
        info = `${parsed.icmp.typeName} code=${parsed.icmp.code}`;
    }

    return (
        <div
            onClick={onClick}
            style={{
                position: 'absolute',
                top: 0,
                left: 0,
                width: '100%',
                minWidth: TOTAL_WIDTH,
                height: ROW_HEIGHT,
                transform: `translateY(${start}px)`,
                display: 'flex',
                alignItems: 'center',
                padding: '0 8px',
                borderBottom: '1px solid var(--g-color-line-generic)',
                backgroundColor: isSelected
                    ? 'var(--g-color-base-selection)'
                    : index % 2 === 0
                        ? 'transparent'
                        : 'var(--g-color-base-generic-ultralight)',
                boxSizing: 'border-box',
                cursor: 'pointer',
            }}
        >
            <div style={cellStyles.index}>{index + 1}</div>
            <div style={cellStyles.time}>{formatTime(packet.timestamp)}</div>
            <div style={cellStyles.source} title={src}>{src || '-'}</div>
            <div style={cellStyles.destination} title={dst}>{dst || '-'}</div>
            <div style={cellStyles.protocol}>{protocol || '-'}</div>
            <div style={cellStyles.length}>{parsed.raw.length}</div>
            <div style={cellStyles.info} title={info}>{info || '-'}</div>
        </div>
    );
};
