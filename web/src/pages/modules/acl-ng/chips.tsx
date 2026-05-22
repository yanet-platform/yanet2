import React from 'react';
import type { Action } from '../../../api/acl-ng';
import { ActionKind, ACTION_KIND_LABELS } from '../../../api/acl-ng';
import { formatIPNet } from '../../../utils';
import { extractBytes } from './utils';

// Protocol names per IANA IP protocol number.
const IP_PROTOS: Record<number, string> = {
    0: 'HOPOPT', 1: 'ICMP', 2: 'IGMP', 4: 'IPv4', 6: 'TCP',
    17: 'UDP', 41: 'IPv6', 43: 'IPv6-Route', 44: 'IPv6-Frag',
    47: 'GRE', 50: 'ESP', 51: 'AH', 58: 'ICMPv6', 59: 'IPv6-NoNxt',
    60: 'IPv6-Opts', 89: 'OSPF', 103: 'PIM', 112: 'VRRP', 132: 'SCTP',
};

type ProtoTone = 'tcp' | 'udp' | 'icmp' | 'misc' | 'dim';

const protoTone = (proto: number): ProtoTone => {
    if (proto === 6) return 'tcp';
    if (proto === 17) return 'udp';
    if (proto === 1 || proto === 58) return 'icmp';
    if (proto === 47 || proto === 50 || proto === 51 || proto === 132) return 'misc';
    return 'dim';
};

interface DecodedProto {
    label: string;
    tone: ProtoTone | 'dim';
    title: string;
}

/** Decode an encoded proto range string "A-B" to a display label + tone. */
const decodeProtoRange = (rangeStr: string): DecodedProto => {
    const m = /^(\d+)\s*-\s*(\d+)$/.exec(rangeStr.trim());
    if (!m) return { label: rangeStr, tone: 'dim', title: rangeStr };
    const from = parseInt(m[1], 10);
    const to = parseInt(m[2], 10);
    const pFrom = from >> 8;
    const pTo = to >> 8;
    const sFrom = from & 0xff;
    const sTo = to & 0xff;

    if (pFrom === pTo) {
        const name = IP_PROTOS[pFrom] ?? `proto ${pFrom}`;
        const tone = protoTone(pFrom);
        if (sFrom === 0 && sTo === 255) {
            return { label: name, tone, title: rangeStr };
        }
        const sub = sFrom === sTo ? String(sFrom) : `${sFrom}-${sTo}`;
        return { label: `${name}/${sub}`, tone, title: rangeStr };
    }
    return { label: `${from}-${to}`, tone: 'dim', title: rangeStr };
};

/** Collapse "N-N" to "N". Returns empty string for empty input. */
const collapseRange = (rangeStr: string): string => {
    const m = /^(\d+)\s*-\s*(\d+)$/.exec(rangeStr.trim());
    if (!m) return rangeStr;
    if (m[1] === m[2]) return m[1];
    return `${m[1]}-${m[2]}`;
};

interface AnyChipProps {
    children?: React.ReactNode;
}

/** Dashed-border muted chip for wildcard values. */
export const AnyChip: React.FC<AnyChipProps> = ({ children = 'any' }) => (
    <span className="acl-chip acl-chip--any">{children}</span>
);

interface ProtoChipProps {
    rangeStr: string;
}

/** Renders a single proto range chip with color based on protocol. */
export const ProtoChip: React.FC<ProtoChipProps> = ({ rangeStr }) => {
    const { label, tone, title } = decodeProtoRange(rangeStr);
    return (
        <span
            className={`acl-chip acl-chip--proto acl-chip--proto-${tone}`}
            title={title}
        >
            {label}
        </span>
    );
};

interface PortRangeChipProps {
    rangeStr: string;
}

/** Renders a single port range chip. Collapses "N-N" to "N". */
export const PortRangeChip: React.FC<PortRangeChipProps> = ({ rangeStr }) => {
    const label = collapseRange(rangeStr);
    return (
        <span className="acl-chip acl-chip--port" title={rangeStr}>
            {label}
        </span>
    );
};

interface VlanRangeChipProps {
    rangeStr: string;
}

/** Renders a single VLAN range chip. */
export const VlanRangeChip: React.FC<VlanRangeChipProps> = ({ rangeStr }) => {
    const label = collapseRange(rangeStr);
    return (
        <span className="acl-chip acl-chip--vlan" title={rangeStr}>
            {label}
        </span>
    );
};

interface IpNetChipProps {
    cidr: string;
}

/** Renders a CIDR chip, colored differently for IPv4 vs IPv6. */
export const IpNetChip: React.FC<IpNetChipProps> = ({ cidr }) => {
    const isV6 = cidr.includes(':');
    return (
        <span
            className={`acl-chip ${isV6 ? 'acl-chip--ipv6' : 'acl-chip--ipv4'}`}
            title={cidr}
        >
            {cidr}
        </span>
    );
};

/** Format an IPNet wire object to a CIDR string. */
export const formatIPNetChip = (net: { addr?: string | Uint8Array | number[]; mask?: string | Uint8Array | number[] }): string => {
    const addrBytes = extractBytes(net.addr);
    const maskBytes = extractBytes(net.mask);
    if (!addrBytes || addrBytes.length === 0) return '';
    return formatIPNet(addrBytes, maskBytes);
};

interface ChipListProps<T> {
    items: T[];
    renderChip: (item: T, idx: number) => React.ReactNode;
    anyLabel?: string;
    isAny?: boolean;
    maxVisible?: number;
}

/** Renders up to maxVisible chips inline, then "+N" overflow indicator. */
export const ChipList = <T,>({
    items,
    renderChip,
    anyLabel = 'any',
    isAny = false,
    maxVisible = 3,
}: ChipListProps<T>): React.ReactElement => {
    if (isAny || items.length === 0) {
        return <AnyChip>{anyLabel}</AnyChip>;
    }
    const visible = items.slice(0, maxVisible);
    const rest = items.length - visible.length;
    return (
        <span className="acl-chip-list" title={String(items)}>
            {visible.map((item, idx) => renderChip(item, idx))}
            {rest > 0 && <span className="acl-chip acl-chip--overflow">+{rest}</span>}
        </span>
    );
};

/** Terminal action kinds (chain ends here). */
const TERMINAL_KINDS = new Set<ActionKind>([
    ActionKind.ACTION_KIND_PASS,
    ActionKind.ACTION_KIND_DENY,
]);

interface ActionChainProps {
    actions: Action[];
}

/** Renders an action chain as chips joined by arrows. */
export const ActionChain: React.FC<ActionChainProps> = ({ actions }) => {
    if (actions.length === 0) {
        return <span className="acl-action-empty">—</span>;
    }

    return (
        <span className="acl-action-chain">
            {actions.map((action, idx) => {
                const kind = action.kind ?? ActionKind.ACTION_KIND_PASS;
                const label = ACTION_KIND_LABELS[kind] ?? String(kind);
                const isTerminal = TERMINAL_KINDS.has(kind);
                const isPass = kind === ActionKind.ACTION_KIND_PASS;
                const isDeny = kind === ActionKind.ACTION_KIND_DENY;

                const isLog = kind === ActionKind.ACTION_KIND_LOG;
                const isState =
                    kind === ActionKind.ACTION_KIND_CREATE_STATE ||
                    kind === ActionKind.ACTION_KIND_CHECK_STATE;

                let cls = 'acl-action-chip';
                if (isPass) cls += ' acl-action-chip--pass';
                else if (isDeny) cls += ' acl-action-chip--deny';
                else if (isLog) cls += ' acl-action-chip--log';
                else if (isState) cls += ' acl-action-chip--state';
                else if (isTerminal) cls += ' acl-action-chip--terminal';

                return (
                    <React.Fragment key={idx}>
                        {idx > 0 && <span className="acl-action-arrow" aria-hidden="true">→</span>}
                        <span className={cls}>{label}</span>
                    </React.Fragment>
                );
            })}
        </span>
    );
};
