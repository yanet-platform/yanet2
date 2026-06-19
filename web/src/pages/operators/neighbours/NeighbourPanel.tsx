import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Bulb, CircleInfo, Layers } from '@gravity-ui/icons';
import { Select } from '@gravity-ui/uikit';
import { useDrawerKeyboard } from '../../../hooks';
import { DotBadge } from '../../../components/VirtualTable';
import { ipAddressToString, stringToIPAddress } from '../../../utils/netip';
import { formatUnixSeconds } from '../../../utils';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { validateMAC, validateNextHop, resolveSubmitTable } from './utils';
import { nudStateToName, getStateMeta } from './stateMeta';
import { getMergeDebug } from './mergeDebug';
import { MERGED_TAB } from './types';

const ZERO_MAC = '00:00:00:00:00:00';

/** Derives the address family label for an IP address string. */
const getFamily = (ip: string): string => {
    if (ip.includes(':')) {
        return ip.toLowerCase().startsWith('fe80') ? 'link-local' : 'ipv6';
    }
    return 'ipv4';
};

interface CopyValProps {
    value: string;
    muted?: boolean;
}

/** Mono value with a clipboard copy button. */
const CopyVal: React.FC<CopyValProps> = ({ value, muted }) => {
    const [copied, setCopied] = useState(false);

    const handleCopy = useCallback((): void => {
        const showSuccess = (): void => {
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1200);
        };

        const legacyCopy = (): boolean => {
            const textarea = document.createElement('textarea');
            textarea.value = value;
            textarea.style.cssText = 'position:fixed;top:-9999px;left:-9999px;opacity:0';
            document.body.appendChild(textarea);
            textarea.focus();
            textarea.select();
            const ok = document.execCommand('copy');
            document.body.removeChild(textarea);
            return ok;
        };

        if (navigator.clipboard?.writeText) {
            navigator.clipboard.writeText(value).then(showSuccess).catch(() => {
                if (legacyCopy()) showSuccess();
            });
        } else if (legacyCopy()) {
            showSuccess();
        }
    }, [value]);

    return (
        <span className="nb-copy-val">
            <span className={`yn-cell-mono${muted ? ' nb-copy-val--muted' : ''}`}>{value}</span>
            <button
                type="button"
                className="nb-copy-btn"
                title={copied ? 'Copied!' : 'Copy'}
                onClick={handleCopy}
                aria-label={`Copy ${value}`}
            >
                {copied ? '✓' : '⧉'}
            </button>
        </span>
    );
};

interface StateBadgeInlineProps {
    state: number | undefined | null;
}

const StateBadgeInline: React.FC<StateBadgeInlineProps> = ({ state }) => {
    const name = nudStateToName(state);
    const meta = getStateMeta(state);
    return <DotBadge label={name} color={meta.color} />;
};

export type NeighbourPanelMode = 'add' | 'edit' | 'view';

export interface NeighbourPanelProps {
    open: boolean;
    mode: NeighbourPanelMode;
    neighbour: Neighbour | null;
    tables: NeighbourTableInfo[];
    defaultTable: string;
    activeTable: string;
    isMergedView: boolean;
    cache: Map<string, Neighbour[]>;
    onClose: () => void;
    onSubmit: (table: string, entry: Neighbour) => Promise<void>;
    /** Called when the user clicks Delete in edit mode — opens a confirmation dialog. */
    onDeleteRequest: (neighbour: Neighbour) => void;
    onPinAsStatic: (neighbour: Neighbour) => void;
}

/** Unified neighbour side panel supporting add, edit, and view modes. */
const NeighbourPanel: React.FC<NeighbourPanelProps> = ({
    open,
    mode,
    neighbour,
    tables,
    defaultTable,
    activeTable,
    isMergedView,
    cache,
    onClose,
    onSubmit,
    onDeleteRequest,
    onPinAsStatic,
}) => {
    const nextHopRef = useRef<HTMLInputElement | null>(null);
    const isMergedAdd = mode === 'add' && activeTable === MERGED_TAB;

    const tableOptions = tables
        .filter((t) => t.name)
        .map((t) => ({ value: t.name!, content: t.name! }));

    const [selectedTable, setSelectedTable] = useState<string[]>([defaultTable]);
    const [nextHop, setNextHop] = useState('');
    const [linkAddr, setLinkAddr] = useState('');
    const [hardwareAddr, setHardwareAddr] = useState('');
    const [device, setDevice] = useState('');
    const [priority, setPriority] = useState('');
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (!open) return;
        setSubmitting(false);
        setSelectedTable([defaultTable]);

        if (neighbour) {
            setNextHop(ipAddressToString(neighbour.next_hop));
            setLinkAddr(neighbour.link_addr?.addr || '');
            setHardwareAddr(neighbour.hardware_addr?.addr || '');
            setDevice(neighbour.device || '');
            setPriority(neighbour.priority?.toString() || '');
        } else {
            setNextHop('');
            setLinkAddr('');
            setHardwareAddr('');
            setDevice('');
            setPriority('');
        }
    }, [open, mode, defaultTable, neighbour]);

    useEffect(() => {
        if (open && mode === 'add') {
            nextHopRef.current?.focus();
        }
    }, [open, mode]);

    const nextHopError = mode === 'add' ? validateNextHop(nextHop) : undefined;
    const linkAddrError = (mode === 'add' || mode === 'edit') ? validateMAC(linkAddr, { required: true }) : undefined;
    const hardwareAddrError = (mode === 'add' || mode === 'edit') ? validateMAC(hardwareAddr, { required: true }) : undefined;

    const canSubmit =
        !submitting &&
        (mode === 'edit' || nextHop.trim() !== '') &&
        !nextHopError &&
        !linkAddrError &&
        !hardwareAddrError;

    const handleApply = async (): Promise<void> => {
        if (!canSubmit || mode === 'view') return;
        setSubmitting(true);
        try {
            const resolvedTable = resolveSubmitTable(mode, activeTable, selectedTable[0], defaultTable, neighbour);

            let nextHopWire: Neighbour['next_hop'];
            if (mode === 'add') {
                nextHopWire = stringToIPAddress(nextHop.trim()) ?? undefined;
                if (!nextHopWire) return;
            } else {
                nextHopWire = neighbour?.next_hop;
            }

            const entry: Neighbour = {
                next_hop: nextHopWire,
                device: device.trim() || undefined,
                priority: priority.trim() ? Number(priority.trim()) : undefined,
            };
            if (linkAddr.trim()) entry.link_addr = { addr: linkAddr.trim() };
            if (hardwareAddr.trim()) entry.hardware_addr = { addr: hardwareAddr.trim() };

            await onSubmit(resolvedTable, entry);
            onClose();
        } finally {
            setSubmitting(false);
        }
    };

    const handleDeleteRequest = (): void => {
        if (!neighbour || mode !== 'edit') return;
        onDeleteRequest(neighbour);
    };

    useDrawerKeyboard({
        open,
        onClose,
        onApply: mode === 'view' ? undefined : () => void handleApply(),
        canApply: canSubmit,
    });

    const ip = neighbour ? (ipAddressToString(neighbour.next_hop) || '—') : '—';
    const family = ip !== '—' ? getFamily(ip) : '';
    const deviceDisplay = neighbour?.device || '—';

    const stateName = nudStateToName(neighbour?.state);
    const meta = getStateMeta(neighbour?.state);
    const neighbourMac = neighbour?.link_addr?.addr || '—';
    const interfaceMac = neighbour?.hardware_addr?.addr || '—';
    const source = neighbour?.source || '—';
    const isStatic = source === 'static';
    const priorityDisplay = neighbour?.priority != null ? String(neighbour.priority) : '—';
    const updatedAt = formatUnixSeconds(neighbour?.updated_at);

    const { shadowed, macConflict } = getMergeDebug(neighbour ?? {}, cache, tables);
    const hasShadowed = shadowed.length > 0;

    const resolvedTableForTitle = resolveSubmitTable(
        mode === 'view' ? 'edit' : mode,
        activeTable,
        selectedTable[0],
        defaultTable,
        neighbour,
    );

    let headerTitle = ip;
    if (mode === 'add' && !neighbour) {
        headerTitle = 'New neighbour';
    }

    const ariaLabel =
        mode === 'add' ? 'Add neighbour' :
        mode === 'edit' ? 'Edit neighbour' :
        'Neighbour details';

    return (
        <>
            <div
                className={`yn-backdrop${open ? ' yn-backdrop--open' : ''}`}
                onClick={onClose}
                aria-hidden="true"
            />
            <aside
                className={`yn-drawer${open ? ' yn-drawer--open' : ''} nb-detail-panel`}
                role="dialog"
                aria-modal="true"
                aria-label={ariaLabel}
            >
                {open && (
                    <>
                        <div className="yn-drawer__head">
                            <div className="nb-detail-panel__header-info">
                                <div className="nb-detail-panel__ip yn-cell-mono">{headerTitle}</div>
                                {mode !== 'add' && (
                                    <div className="nb-detail-panel__subtitle">
                                        on {deviceDisplay}{family ? ` · ${family}` : ''}
                                    </div>
                                )}
                                {mode === 'add' && resolvedTableForTitle && (
                                    <div className="nb-detail-panel__subtitle">
                                        adding to {resolvedTableForTitle}
                                    </div>
                                )}
                            </div>
                            {mode !== 'add' && neighbour && (
                                <StateBadgeInline state={neighbour.state} />
                            )}
                            <button
                                type="button"
                                className="yn-icon-btn"
                                onClick={onClose}
                                aria-label="Close panel"
                            >
                                ✕
                            </button>
                        </div>

                        <div className="yn-drawer__body">
                            {mode === 'view' && neighbour && (
                                <>
                                    <div className="yn-section">
                                        <div className="yn-section-h">Resolved entry</div>
                                        <dl className="nb-kv">
                                            <dt>Next hop</dt>
                                            <dd><CopyVal value={ip} /></dd>
                                            <dt>Neighbour MAC</dt>
                                            <dd>
                                                <CopyVal value={neighbourMac} muted={neighbourMac === ZERO_MAC} />
                                            </dd>
                                            <dt>Interface MAC</dt>
                                            <dd>
                                                <CopyVal value={interfaceMac} muted={interfaceMac === ZERO_MAC} />
                                            </dd>
                                            <dt>Device</dt>
                                            <dd className="yn-cell-mono">{deviceDisplay}</dd>
                                            <dt>Source</dt>
                                            <dd>
                                                <span className={`nb-src-name${isStatic ? ' nb-src-name--static' : ''}`}>
                                                    {source}
                                                </span>
                                                {' · priority '}{priorityDisplay}
                                            </dd>
                                            <dt>Updated at</dt>
                                            <dd className="yn-cell-mono">{updatedAt}</dd>
                                        </dl>
                                    </div>

                                    <div className="yn-section">
                                        <div className="yn-section-h">State — {stateName}</div>
                                        <div className="nb-state-explain">
                                            <div className="nb-state-explain__head">
                                                <StateBadgeInline state={neighbour.state} />
                                            </div>
                                            <p className="nb-state-explain__desc">{meta.desc}</p>
                                            <div className="nb-state-explain__action">
                                                <span className="nb-state-explain__action-icon" style={{ color: 'var(--yn-accent)' }}><Bulb width={13} height={13} /></span>
                                                <span>{meta.action}</span>
                                            </div>
                                        </div>
                                    </div>

                                    {isMergedView && (
                                        <div className="yn-section">
                                            <div className="yn-section-h">Merge resolution</div>
                                            {hasShadowed ? (
                                                <div className="nb-why-box">
                                                    <span className="nb-why-box__icon" style={{ color: 'var(--yn-accent)' }}><Layers width={13} height={13} /></span>
                                                    <p>
                                                        <strong>{source}</strong> wins with the lowest priority{' '}
                                                        <strong>{priorityDisplay}</strong> (higher precedence), shadowing {shadowed.length}{' '}
                                                        {shadowed.length === 1 ? 'entry' : 'entries'} from{' '}
                                                        <strong>{shadowed.map((s) => s.table).join(', ')}</strong>.
                                                        {macConflict && ' Note the MAC differs between tables.'}
                                                    </p>
                                                </div>
                                            ) : (
                                                <div className="nb-why-box">
                                                    <span className="nb-why-box__icon" style={{ color: 'var(--yn-text-3)' }}><CircleInfo width={13} height={13} /></span>
                                                    <p>
                                                        Only one table provides this neighbour (<strong>{source}</strong>).
                                                        Nothing to merge.
                                                    </p>
                                                </div>
                                            )}

                                            <div className="nb-cand-stack">
                                                <div className="nb-cand nb-cand--winner">
                                                    <div className="nb-cand__top">
                                                        <span className="nb-cand__table yn-cell-mono">{source}</span>
                                                        <span className="nb-cand__tag nb-cand__tag--winner">winner</span>
                                                        <span className="nb-cand__prio yn-cell-mono">priority {priorityDisplay}</span>
                                                    </div>
                                                    <div className="nb-cand__grid">
                                                        <div className="nb-cand__cell">
                                                            <span>State</span>
                                                            <StateBadgeInline state={neighbour.state} />
                                                        </div>
                                                        <div className="nb-cand__cell">
                                                            <span>Updated</span>
                                                            <b className="yn-cell-mono">{updatedAt}</b>
                                                        </div>
                                                        <div className="nb-cand__cell">
                                                            <span>Neighbour MAC</span>
                                                            <b className="yn-cell-mono">{neighbourMac}</b>
                                                        </div>
                                                        <div className="nb-cand__cell">
                                                            <span>Interface MAC</span>
                                                            <b className="yn-cell-mono">{interfaceMac}</b>
                                                        </div>
                                                    </div>
                                                </div>

                                                {shadowed.map((cand) => {
                                                    const candMac = cand.entry.link_addr?.addr || '—';
                                                    const candImac = cand.entry.hardware_addr?.addr || '—';
                                                    const candUpdated = formatUnixSeconds(cand.entry.updated_at);
                                                    return (
                                                        <div key={cand.table} className="nb-cand">
                                                            <div className="nb-cand__top">
                                                                <span className="nb-cand__table yn-cell-mono">{cand.table}</span>
                                                                <span className="nb-cand__tag nb-cand__tag--shadow">shadowed</span>
                                                                <span className="nb-cand__prio yn-cell-mono">priority {cand.priority}</span>
                                                            </div>
                                                            <div className="nb-cand__grid">
                                                                <div className="nb-cand__cell">
                                                                    <span>State</span>
                                                                    <StateBadgeInline state={cand.entry.state} />
                                                                </div>
                                                                <div className="nb-cand__cell">
                                                                    <span>Updated</span>
                                                                    <b className="yn-cell-mono">{candUpdated}</b>
                                                                </div>
                                                                <div className="nb-cand__cell">
                                                                    <span>Neighbour MAC</span>
                                                                    <b className={`yn-cell-mono${cand.macDiffers ? ' nb-cand__diff' : ''}`}>
                                                                        {candMac}
                                                                    </b>
                                                                </div>
                                                                <div className="nb-cand__cell">
                                                                    <span>Interface MAC</span>
                                                                    <b className="yn-cell-mono">{candImac}</b>
                                                                </div>
                                                            </div>
                                                        </div>
                                                    );
                                                })}
                                            </div>
                                        </div>
                                    )}
                                </>
                            )}

                            {(mode === 'add' || mode === 'edit') && (
                                <>
                                    {isMergedAdd && (
                                        <section className="yn-section">
                                            <div className="yn-section-h">Target</div>
                                            <div className="yn-section__body">
                                                <div className="yn-field">
                                                    <label className="yn-field__label">
                                                        Table <span className="yn-field__req">*</span>
                                                    </label>
                                                    <Select
                                                        value={selectedTable}
                                                        onUpdate={setSelectedTable}
                                                        options={tableOptions}
                                                        width="max"
                                                    />
                                                </div>
                                            </div>
                                        </section>
                                    )}

                                    <section className="yn-section">
                                        <div className="yn-section-h">Identity</div>
                                        <div className="yn-section__body">
                                            <div className="yn-field">
                                                <label className="yn-field__label">
                                                    Next Hop
                                                    {mode === 'add' && <span className="yn-field__req">*</span>}
                                                </label>
                                                <input
                                                    ref={nextHopRef}
                                                    className={`yn-input yn-input--mono${nextHopError ? ' yn-input--invalid' : ''}`}
                                                    value={nextHop}
                                                    placeholder="192.168.1.1 or fe80::1"
                                                    onChange={(e) => setNextHop(e.target.value)}
                                                    disabled={mode === 'edit'}
                                                />
                                                {nextHopError && (
                                                    <span className="yn-field__hint yn-field__error">{nextHopError}</span>
                                                )}
                                                {mode === 'edit' && (
                                                    <span className="yn-field__hint">Primary key — delete and recreate to change.</span>
                                                )}
                                            </div>
                                        </div>
                                    </section>

                                    <section className="yn-section">
                                        <div className="yn-section-h">L2</div>
                                        <div className="yn-section__body">
                                            <div className="yn-field">
                                                <label className="yn-field__label">
                                                    Neighbour MAC <span className="yn-field__req">*</span>
                                                </label>
                                                <input
                                                    className={`yn-input yn-input--mono${linkAddrError ? ' yn-input--invalid' : ''}`}
                                                    value={linkAddr}
                                                    placeholder="52:54:00:12:34:56"
                                                    onChange={(e) => setLinkAddr(e.target.value)}
                                                />
                                                {linkAddrError && (
                                                    <span className="yn-field__hint yn-field__error">{linkAddrError}</span>
                                                )}
                                            </div>
                                            <div className="yn-field">
                                                <label className="yn-field__label">
                                                    Interface MAC <span className="yn-field__req">*</span>
                                                </label>
                                                <input
                                                    className={`yn-input yn-input--mono${hardwareAddrError ? ' yn-input--invalid' : ''}`}
                                                    value={hardwareAddr}
                                                    placeholder="52:54:00:12:34:56"
                                                    onChange={(e) => setHardwareAddr(e.target.value)}
                                                />
                                                {hardwareAddrError && (
                                                    <span className="yn-field__hint yn-field__error">{hardwareAddrError}</span>
                                                )}
                                            </div>
                                        </div>
                                    </section>

                                    <section className="yn-section">
                                        <div className="yn-section-h">Egress</div>
                                        <div className="yn-section__body">
                                            <div className="yn-field">
                                                <label className="yn-field__label">Device</label>
                                                <input
                                                    className="yn-input"
                                                    value={device}
                                                    placeholder="eth0"
                                                    onChange={(e) => setDevice(e.target.value)}
                                                />
                                            </div>
                                            <div className="yn-field">
                                                <label className="yn-field__label">Priority</label>
                                                <input
                                                    className="yn-input"
                                                    type="number"
                                                    value={priority}
                                                    placeholder="100"
                                                    onChange={(e) => setPriority(e.target.value)}
                                                />
                                            </div>
                                        </div>
                                    </section>

                                    {mode === 'edit' && neighbour && (
                                        <div className="yn-section">
                                            <div className="yn-section-h">State — {stateName}</div>
                                            <div className="nb-state-explain">
                                                <div className="nb-state-explain__head">
                                                    <StateBadgeInline state={neighbour.state} />
                                                </div>
                                                <p className="nb-state-explain__desc">{meta.desc}</p>
                                                <div className="nb-state-explain__action">
                                                    <span className="nb-state-explain__action-icon" style={{ color: 'var(--yn-accent)' }}><Bulb width={13} height={13} /></span>
                                                    <span>{meta.action}</span>
                                                </div>
                                            </div>
                                        </div>
                                    )}
                                </>
                            )}
                        </div>

                        <div className="yn-drawer__foot">
                            {mode === 'add' && (
                                <>
                                    <div className="yn-drawer__foot-actions">
                                        <button
                                            type="button"
                                            className="yn-btn yn-btn--ghost yn-btn--sm"
                                            onClick={onClose}
                                        >
                                            Cancel
                                        </button>
                                        <button
                                            type="button"
                                            className="yn-btn yn-btn--primary yn-btn--sm"
                                            onClick={() => void handleApply()}
                                            disabled={!canSubmit}
                                        >
                                            Add{resolvedTableForTitle ? ` to ${resolvedTableForTitle}` : ''}
                                        </button>
                                    </div>
                                </>
                            )}

                            {mode === 'edit' && (
                                <>
                                    <div className="yn-drawer__foot-actions">
                                        <button
                                            type="button"
                                            className="yn-btn yn-btn--danger yn-btn--sm"
                                            onClick={handleDeleteRequest}
                                            disabled={submitting}
                                        >
                                            Delete
                                        </button>
                                    </div>
                                    <div className="yn-drawer__foot-actions">
                                        <button
                                            type="button"
                                            className="yn-btn yn-btn--ghost yn-btn--sm"
                                            onClick={onClose}
                                        >
                                            Cancel
                                        </button>
                                        <button
                                            type="button"
                                            className="yn-btn yn-btn--primary yn-btn--sm"
                                            onClick={() => void handleApply()}
                                            disabled={!canSubmit}
                                        >
                                            Apply
                                        </button>
                                    </div>
                                </>
                            )}

                            {mode === 'view' && (
                                <>
                                    <div className="yn-drawer__foot-actions">
                                        {!isStatic && neighbour && (
                                            <button
                                                type="button"
                                                className="yn-btn yn-btn--ghost yn-btn--sm"
                                                onClick={() => onPinAsStatic(neighbour)}
                                            >
                                                📌 Pin as static
                                            </button>
                                        )}
                                    </div>
                                    <button
                                        type="button"
                                        className="yn-btn yn-btn--ghost yn-btn--sm"
                                        onClick={onClose}
                                    >
                                        Close
                                    </button>
                                </>
                            )}
                        </div>
                    </>
                )}
            </aside>
        </>
    );
};

export default NeighbourPanel;
