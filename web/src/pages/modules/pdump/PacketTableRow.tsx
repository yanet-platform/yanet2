import React from 'react';
import { formatTCPFlags } from '../../../utils';
import { cellStyles, TOTAL_WIDTH, ROW_HEIGHT } from './constants';
import type { CapturedPacket } from './types';

export interface PacketTableRowProps {
    packet: CapturedPacket;
    index: number;
    start: number;
    isSelected: boolean;
    isNew: boolean;
    onSelect: (packet: CapturedPacket) => void;
}

const formatTime = (date: Date): string => {
    const pad = (n: number, len: number = 2) => n.toString().padStart(len, '0');
    return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`;
};

const getProtocolClass = (protocol: string): string => {
    const p = protocol.toLowerCase();
    if (p === 'tcp') return 'pdump-proto-tcp';
    if (p === 'udp') return 'pdump-proto-udp';
    if (p === 'icmp' || p === 'icmpv6' || p === 'icmp6') return 'pdump-proto-icmp';
    if (p === 'arp') return 'pdump-proto-arp';
    return '';
};

const PacketTableRowImpl: React.FC<PacketTableRowProps> = ({
    packet,
    index,
    start,
    isSelected,
    isNew,
    onSelect,
}) => {
    const { parsed } = packet;

    let src = '';
    let dst = '';
    let protocol = '';

    if (parsed.ipv4) {
        src = parsed.ipv4.srcAddr;
        dst = parsed.ipv4.dstAddr;
        protocol = parsed.ipv4.protocolName;
    } else if (parsed.ipv6) {
        src = `[${parsed.ipv6.srcAddr}]`;
        dst = `[${parsed.ipv6.dstAddr}]`;
        protocol = parsed.ipv6.nextHeaderName;
    } else if (parsed.ethernet) {
        src = parsed.ethernet.srcMac;
        dst = parsed.ethernet.dstMac;
        protocol = parsed.ethernet.etherTypeName;
    }

    if (parsed.tcp) {
        src = `${src}:${parsed.tcp.srcPort}`;
        dst = `${dst}:${parsed.tcp.dstPort}`;
        protocol = 'TCP';
    } else if (parsed.udp) {
        src = `${src}:${parsed.udp.srcPort}`;
        dst = `${dst}:${parsed.udp.dstPort}`;
        protocol = 'UDP';
    } else if (parsed.icmp) {
        protocol = 'ICMP';
    }

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

    const protoClass = getProtocolClass(protocol);

    const classes = [
        'packet-table__row',
        isNew ? 'packet-table__row--new' : '',
        isSelected ? 'packet-table__row--selected' : '',
    ].filter(Boolean).join(' ');

    return (
        <div
            onClick={() => onSelect(packet)}
            className={classes}
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
                borderBottom: '1px solid var(--fw-line-2)',
                backgroundColor: index % 2 === 0 ? 'transparent' : 'var(--fw-bg-2)',
                boxSizing: 'border-box',
                cursor: 'pointer',
            }}
        >
            <div style={cellStyles.index}>{packet.id + 1}</div>
            <div style={cellStyles.time}>{formatTime(packet.timestamp)}</div>
            <div style={cellStyles.source} title={src}>{src || '-'}</div>
            <div style={cellStyles.destination} title={dst}>{dst || '-'}</div>
            <div style={cellStyles.protocol}>
                {protocol ? (
                    <span className={`pdump-proto-badge${protoClass ? ` ${protoClass}` : ''}`}>
                        {protocol}
                    </span>
                ) : '-'}
            </div>
            <div style={cellStyles.length}>{parsed.raw.length}</div>
            <div style={cellStyles.info} title={info}>{info || '-'}</div>
        </div>
    );
};

export const PacketTableRow = React.memo(PacketTableRowImpl);
