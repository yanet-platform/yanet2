import React, { useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { Action } from '../../../api/acl-ng';
import { ActionKind, ACTION_KIND_LABELS } from '../../../api/acl-ng';
import { formatIPNet, toaster, copyToClipboard } from '../../../utils';
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

interface ChipListModalProps<T> {
    items: T[];
    renderChip: (item: T, idx: number) => React.ReactNode;
    label: string;
    getItemText: (item: T) => string;
    onClose: () => void;
}

/** Full-list modal for a ChipList overflow, rendered via a portal onto document.body. */
const ChipListModal = <T,>({
    items,
    renderChip,
    label,
    getItemText,
    onClose,
}: ChipListModalProps<T>): React.ReactElement => {
    const [query, setQuery] = useState('');
    const searchRef = useRef<HTMLInputElement | null>(null);

    const queryLower = query.trim().toLowerCase();
    const filtered = queryLower
        ? items.filter(item => getItemText(item).toLowerCase().includes(queryLower))
        : items;

    // Compute v4/v6 stats for CIDR-style items (heuristic: any item containing ':').
    const isCidr = items.length > 0 && items.some(item => getItemText(item).includes('/'));
    let statsText: string;
    if (isCidr) {
        let v4 = 0;
        let v6 = 0;
        for (const item of items) {
            if (getItemText(item).includes(':')) v6++;
            else v4++;
        }
        statsText = `${items.length} total · ${v4} v4 · ${v6} v6`;
    } else {
        statsText = `${items.length} ${label}`;
    }

    const handleBackdropClick = useCallback((e: React.MouseEvent<HTMLDivElement>): void => {
        if (e.target === e.currentTarget) onClose();
    }, [onClose]);

    const handleCopyAll = useCallback((): void => {
        const text = items.map(item => getItemText(item)).join('\n');
        copyToClipboard(text)
            .then(() => toaster.success('acl-ng-chip-copy', 'Copied.'))
            .catch((err) => toaster.error('acl-ng-chip-copy', 'Copy failed.', err));
    }, [items, getItemText]);

    useEffect(() => {
        const onKey = (e: KeyboardEvent): void => {
            if (e.key === 'Escape') onClose();
        };
        document.addEventListener('keydown', onKey);
        return () => document.removeEventListener('keydown', onKey);
    }, [onClose]);

    const modal = (
        <div className="fw-modal-backdrop acl-chip-modal-backdrop" onClick={handleBackdropClick}>
            <div
                className="fw-modal"
                style={{ maxWidth: 560, maxHeight: '70vh', display: 'flex', flexDirection: 'column' }}
                onClick={(e) => e.stopPropagation()}
            >
                <header className="fw-modal__head" style={{ flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
                    <div style={{ display: 'flex', width: '100%', alignItems: 'center', justifyContent: 'space-between' }}>
                        <span className="fw-modal__title" style={{ textTransform: 'capitalize' }}>{label}</span>
                        <button type="button" className="fw-icon-btn" onClick={onClose} aria-label="Close">✕</button>
                    </div>
                    <span className="fw-modal__meta">{statsText}</span>
                </header>

                <div className="fw-modal__body" style={{ gap: 8 }}>
                    <input
                        ref={searchRef}
                        autoFocus
                        type="text"
                        className="acl-chip-modal-search"
                        placeholder={`Filter ${items.length} ${label}…`}
                        value={query}
                        onChange={(e) => setQuery(e.target.value)}
                    />

                    {filtered.length === 0 ? (
                        <div style={{
                            flex: 1,
                            display: 'flex',
                            alignItems: 'center',
                            justifyContent: 'center',
                            color: 'var(--fw-text-3)',
                            fontSize: 13,
                            minHeight: 80,
                            border: '1px solid var(--fw-line)',
                            borderRadius: 6,
                        }}>
                            No matches.
                        </div>
                    ) : (
                        <div style={{
                            flex: 1,
                            overflow: 'auto',
                            display: 'flex',
                            flexWrap: 'wrap',
                            gap: 6,
                            padding: 8,
                            alignContent: 'flex-start',
                            border: '1px solid var(--fw-line)',
                            borderRadius: 6,
                            background: 'var(--fw-bg-2)',
                        }}>
                            {filtered.map((item, idx) => renderChip(item, idx))}
                        </div>
                    )}
                </div>

                <footer className="fw-modal__foot">
                    <span className="fw-modal__foot-hint">
                        Showing {filtered.length} of {items.length}
                    </span>
                    <div className="fw-modal__foot-actions">
                        <button
                            type="button"
                            className="fw-btn fw-btn--ghost fw-btn--sm"
                            onClick={handleCopyAll}
                        >
                            Copy all
                        </button>
                        <button
                            type="button"
                            className="fw-btn fw-btn--primary fw-btn--sm"
                            onClick={onClose}
                        >
                            Close
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );

    return createPortal(modal, document.body) as React.ReactElement;
};

interface ChipListProps<T> {
    items: T[];
    renderChip: (item: T, idx: number) => React.ReactNode;
    anyLabel?: string;
    isAny?: boolean;
    /** Number of chips shown inline before the +N overflow button appears (Mode A). Default 2. */
    inline?: number;
    /** Item count at which Mode B (single summary chip) kicks in instead of Mode A. Default 4. */
    summarizeAt?: number;
    /** Controls summary chip text format: 'cidr' uses v4/v6 split, 'generic' uses label. Default 'generic'. */
    summaryKind?: 'cidr' | 'generic';
    label?: string;
    getItemText?: (item: T) => string;
}

/** Renders chips in one of three modes:
 * - isAny / empty: single "any" chip.
 * - items.length <= summarizeAt: first `inline` chips + optional +N overflow button (Mode A).
 * - items.length > summarizeAt: single summary chip that opens the modal (Mode B).
 */
export const ChipList = <T,>({
    items,
    renderChip,
    anyLabel = 'any',
    isAny = false,
    inline = 2,
    summarizeAt = 4,
    summaryKind = 'generic',
    label = 'items',
    getItemText,
}: ChipListProps<T>): React.ReactElement => {
    const [modalOpen, setModalOpen] = useState(false);

    const resolvedGetItemText = useCallback((item: T): string => {
        if (getItemText) return getItemText(item);
        return String(item);
    }, [getItemText]);

    const handleOverflowClick = useCallback((e: React.MouseEvent): void => {
        e.stopPropagation();
        setModalOpen(true);
    }, []);

    const handleModalClose = useCallback((): void => {
        setModalOpen(false);
    }, []);

    if (isAny || items.length === 0) {
        return <AnyChip>{anyLabel}</AnyChip>;
    }

    // Mode B: large list — single summary chip opens the modal.
    if (items.length > summarizeAt) {
        let summaryLabel: string;
        if (summaryKind === 'cidr') {
            let v4 = 0;
            let v6 = 0;
            for (const it of items) {
                if (resolvedGetItemText(it).includes(':')) v6++;
                else v4++;
            }
            if (v4 && v6) summaryLabel = `${items.length} CIDRs · v4·${v4} v6·${v6}`;
            else if (v6) summaryLabel = `${items.length} v6 CIDRs`;
            else summaryLabel = `${items.length} v4 CIDRs`;
        } else {
            summaryLabel = `${items.length} ${label}`;
        }
        return (
            <span className="acl-chip-list">
                <button
                    type="button"
                    className="acl-chip acl-chip--summary acl-chip--trigger"
                    onClick={handleOverflowClick}
                    title="Show full list"
                >
                    {summaryLabel}
                </button>
                {modalOpen && (
                    <ChipListModal
                        items={items}
                        renderChip={renderChip}
                        label={label}
                        getItemText={resolvedGetItemText}
                        onClose={handleModalClose}
                    />
                )}
            </span>
        );
    }

    // Mode A: small list — render first `inline` chips + optional +N overflow.
    const visible = items.slice(0, inline);
    const rest = items.length - visible.length;
    return (
        <span className="acl-chip-list" title={String(items)}>
            {visible.map((item, idx) => renderChip(item, idx))}
            {rest > 0 && (
                <button
                    type="button"
                    className="acl-chip acl-chip--overflow acl-chip--trigger"
                    onClick={handleOverflowClick}
                    title="Show all"
                >
                    +{rest}
                </button>
            )}
            {modalOpen && (
                <ChipListModal
                    items={items}
                    renderChip={renderChip}
                    label={label}
                    getItemText={resolvedGetItemText}
                    onClose={handleModalClose}
                />
            )}
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
