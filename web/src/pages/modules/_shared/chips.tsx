import React from 'react';
import './chips.scss';

export type ProtoTone = 'tcp' | 'udp' | 'icmp' | 'misc' | 'dim';

export interface ProtoDisplay {
    label: string;
    tone: ProtoTone;
    title: string;
}

const IP_PROTOCOL_NAMES: Record<number, string> = {
    1: 'ICMP',
    6: 'TCP',
    17: 'UDP',
    41: 'IPv6',
    47: 'GRE',
    50: 'ESP',
    51: 'AH',
    58: 'ICMPv6',
    89: 'OSPF',
    103: 'PIM',
    112: 'VRRP',
    132: 'SCTP',
};

export const getProtocolDisplay = (proto: number | undefined): ProtoDisplay => {
    if (proto === undefined || !Number.isInteger(proto) || proto < 0) {
        return {
            label: '-',
            tone: 'dim',
            title: '-',
        };
    }

    const protocol = proto;

    return {
        label: IP_PROTOCOL_NAMES[protocol] ?? `proto ${protocol}`,
        tone: getProtocolTone(protocol),
        title: IP_PROTOCOL_NAMES[protocol] ?? `proto ${protocol}`,
    };
};

export const getProtocolTone = (proto: number): ProtoTone => {
    if (proto === 6) return 'tcp';
    if (proto === 17) return 'udp';
    if (proto === 1 || proto === 58) return 'icmp';
    if (proto === 47 || proto === 50 || proto === 51 || proto === 132) return 'misc';
    return 'dim';
};

interface IpAddressChipProps {
    value: string;
    family?: 'ipv4' | 'ipv6';
    className?: string;
    title?: string;
}

export const IpAddressChip: React.FC<IpAddressChipProps> = ({
    value,
    family,
    className = '',
    title,
}) => {
    const chipFamily = family ?? (value.includes(':') ? 'ipv6' : 'ipv4');
    return (
        <span
            className={`shared-chip shared-chip--ip shared-chip--ip-${chipFamily} ${className}`.trim()}
            title={title ?? value}
        >
            {value}
        </span>
    );
};

interface ProtoChipProps {
    label: string;
    tone: ProtoTone;
    className?: string;
    title?: string;
}

export const ProtoChip: React.FC<ProtoChipProps> = ({
    label,
    tone,
    className = '',
    title,
}) => {
    return (
        <span
            className={`shared-chip shared-chip--proto shared-chip--proto-${tone} ${className}`.trim()}
            title={title ?? label}
        >
            {label}
        </span>
    );
};

interface ProtocolNumberChipProps {
    proto?: number;
    className?: string;
}

export const ProtocolNumberChip: React.FC<ProtocolNumberChipProps> = ({ proto, className }) => {
    const { label, tone, title } = getProtocolDisplay(proto);
    return <ProtoChip label={label} tone={tone} title={title} className={className} />;
};
