import React, { useCallback, useEffect, useMemo, useRef, useState, memo } from 'react';
import { useContainerHeight } from '../../../hooks';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox, Icon } from '@gravity-ui/uikit';
import { Pause, Play } from '@gravity-ui/icons';
import type { RuleItem } from './types';
import { expandRuleItem, effectiveCounterName, deadReasonText } from './hooks';
import {
    AnyChip,
    IpNetChip,
    PortRangeChip,
    VlanRangeChip,
    ProtoChip,
    ActionChain,
    ChipList,
} from './chips';
import { DraftActionButtons } from '../../_shared/draft';
import type { RuleRate } from './useAclRuleCounters';
import Sparkline from './Sparkline';

export const DRAWER_TRANSITION_MS = 220;

const ROW_HEIGHT = 44;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

const COLUMN_WIDTHS = {
    checkbox: 38,
    index: 48,
    srcs: 180,
    dsts: 180,
    src_ports: 130,
    dst_ports: 130,
    protos: 150,
    vlans: 110,
    devices: 130,
    counter: 140,
    actions: 190,
    sparkline: 110,
} as const;

const TOTAL_WIDTH = Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

type ColKey = keyof typeof COLUMN_WIDTHS;

const cellStyle = (col: ColKey): React.CSSProperties => ({
    width: COLUMN_WIDTHS[col],
    minWidth: COLUMN_WIDTHS[col],
    maxWidth: COLUMN_WIDTHS[col],
    flexShrink: 0,
    overflow: 'hidden',
    paddingRight: col === 'checkbox' ? 0 : 8,
    display: 'flex',
    alignItems: 'center',
    justifyContent: col === 'checkbox' || col === 'index' ? 'center' : 'flex-start',
});

interface VirtualRowProps {
    item: RuleItem;
    start: number;
    selected: boolean;
    active: boolean;
    rate: RuleRate | undefined;
    counterEnabled: boolean;
    onToggleSelect: (id: string) => void;
    onHoverChange: (item: RuleItem | null, start: number) => void;
    onToggleCounter: (counterName: string) => void;
}

const VirtualRow: React.FC<VirtualRowProps> = memo(({
    item,
    start,
    selected,
    active,
    rate,
    counterEnabled,
    onToggleSelect,
    onHoverChange,
    onToggleCounter,
}) => {
    // Expand the rule lazily — only called for the ~25 visible rows at a time.
    const expanded = useMemo(() => expandRuleItem(item.rule), [item.rule]);

    const handleCheckboxChange = useCallback((_checked: boolean): void => {
        onToggleSelect(item.id);
    }, [onToggleSelect, item.id]);

    const handleMouseEnter = useCallback((): void => {
        onHoverChange(item, start);
    }, [onHoverChange, item, start]);

    const handleMouseLeave = useCallback((): void => {
        onHoverChange(null, 0);
    }, [onHoverChange]);

    let rowBg = 'transparent';
    if (active || selected) rowBg = 'var(--fw-accent-soft)';

    return (
        <div
            className={`fw-vrow${selected ? ' fw-vrow--selected' : ''}${active ? ' fw-vrow--active' : ''}`}
            onMouseEnter={handleMouseEnter}
            onMouseLeave={handleMouseLeave}
            style={{
                position: 'absolute',
                top: start,
                left: 0,
                height: ROW_HEIGHT,
                minWidth: TOTAL_WIDTH,
                width: '100%',
                display: 'flex',
                alignItems: 'center',
                borderBottom: '1px solid var(--fw-line)',
                backgroundColor: rowBg,
                paddingLeft: 4,
            }}
        >
            <div style={cellStyle('checkbox')} onClick={e => e.stopPropagation()}>
                <Checkbox
                    checked={selected}
                    onUpdate={handleCheckboxChange}
                    aria-label={`Select rule ${item.index + 1}`}
                />
            </div>

            <div style={{ ...cellStyle('index'), color: 'var(--fw-text-3)', fontVariantNumeric: 'tabular-nums', flexDirection: 'column', gap: 2 }}>
                <span style={{ fontSize: 12 }}>{item.index + 1}</span>
                {expanded.isDead && (
                    <span
                        className="acl-rule-badge acl-rule-badge--dead"
                        title={deadReasonText(expanded)}
                    >
                        dead
                    </span>
                )}
                {!expanded.isDead && expanded.isL2 && (
                    <span
                        className="acl-rule-badge acl-rule-badge--l2"
                        title="No IP filter — matches L2 frames per VLAN/device"
                    >
                        L2
                    </span>
                )}
            </div>

            <div style={cellStyle('srcs')}>
                {expanded.isEmptySrc
                    ? <span className="fw-cell-mono fw-cell-muted">—</span>
                    : <ChipList
                        items={expanded.sourceCidrs}
                        renderChip={(cidr, idx) => <IpNetChip key={idx} cidr={cidr} />}
                        label="sources"
                        inline={2}
                        summarizeAt={4}
                        summaryKind="cidr"
                        getItemText={(cidr) => cidr}
                    />
                }
            </div>

            <div style={cellStyle('dsts')}>
                {expanded.dstCidrs.length === 0
                    ? <span className="fw-cell-mono fw-cell-muted">—</span>
                    : <ChipList
                        items={expanded.dstCidrs}
                        renderChip={(cidr, idx) => <IpNetChip key={idx} cidr={cidr} />}
                        label="destinations"
                        inline={2}
                        summarizeAt={4}
                        summaryKind="cidr"
                        getItemText={(cidr) => cidr}
                    />
                }
            </div>

            <div style={cellStyle('src_ports')}>
                {expanded.isAnySrcPort
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.srcPortRanges}
                        renderChip={(r, idx) => <PortRangeChip key={idx} rangeStr={r} />}
                        label="port ranges"
                        inline={2}
                        summarizeAt={4}
                    />
                }
            </div>

            <div style={cellStyle('dst_ports')}>
                {expanded.isAnyDstPort
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.dstPortRanges}
                        renderChip={(r, idx) => <PortRangeChip key={idx} rangeStr={r} />}
                        label="port ranges"
                        inline={2}
                        summarizeAt={4}
                    />
                }
            </div>

            <div style={cellStyle('protos')}>
                {expanded.protoRanges.length === 0
                    ? <span className="fw-cell-mono fw-cell-muted">—</span>
                    : <ChipList
                        items={expanded.protoRanges}
                        isAny={expanded.isAnyProto}
                        anyLabel="any"
                        renderChip={(r, idx) => <ProtoChip key={idx} rangeStr={r} />}
                        label="protocols"
                        inline={2}
                        summarizeAt={4}
                    />
                }
            </div>

            <div style={cellStyle('vlans')}>
                {expanded.isAnyVlan
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.vlanRanges}
                        renderChip={(r, idx) => <VlanRangeChip key={idx} rangeStr={r} />}
                        label="VLAN ranges"
                        inline={2}
                        summarizeAt={4}
                    />
                }
            </div>

            <div style={cellStyle('devices')}>
                {expanded.deviceNames.length === 0
                    ? <AnyChip>any</AnyChip>
                    : <ChipList
                        items={expanded.deviceNames}
                        renderChip={(d, idx) => (
                            <span key={idx} className="acl-chip acl-chip--device" title={d}>{d}</span>
                        )}
                        label="devices"
                        inline={1}
                        summarizeAt={3}
                    />
                }
            </div>

            <div style={cellStyle('counter')} title={item.counter || `rule ${item.index} (default)`}>
                {item.counter
                    ? <span className="fw-cell-mono">{item.counter}</span>
                    : <span className="fw-cell-mono fw-cell-muted">rule {item.index}</span>
                }
            </div>

            <div style={{ ...cellStyle('sparkline'), gap: 4 }}>
                {counterEnabled ? (
                    <>
                        {rate ? (
                            <>
                                <Sparkline values={rate.history} width={52} height={16} />
                                <span className="fw-cell-pps" title={`${rate.pps.toFixed(0)} pps`}>
                                    {rate.pps >= 1000
                                        ? `${(rate.pps / 1000).toFixed(1)}k`
                                        : rate.pps.toFixed(0)}
                                </span>
                            </>
                        ) : (
                            <span className="fw-cell-pps acl-pps-loading" title="Waiting for counter data">…</span>
                        )}
                        <button
                            type="button"
                            className="acl-counter-toggle acl-counter-toggle--on"
                            onClick={() => onToggleCounter(effectiveCounterName(item.rule, item.index))}
                            title="Stop tracking this counter"
                            aria-label="Disable counter"
                        >
                            <Icon data={Pause} size={16} />
                        </button>
                    </>
                ) : (
                    <>
                        <span className="fw-cell-pps" style={{ color: 'var(--fw-text-3)' }}>—</span>
                        <button
                            type="button"
                            className={`acl-counter-toggle acl-counter-toggle--off${!item.counter ? ' acl-counter-toggle--default' : ''}`}
                            onClick={() => onToggleCounter(effectiveCounterName(item.rule, item.index))}
                            title={`Track counter "${effectiveCounterName(item.rule, item.index)}"`}
                            aria-label="Enable counter"
                        >
                            <Icon data={Play} size={16} />
                        </button>
                    </>
                )}
            </div>

            <div style={cellStyle('actions')}>
                <ActionChain actions={item.rule.actions ?? []} />
            </div>
        </div>
    );
});

VirtualRow.displayName = 'VirtualRow';

interface RuleTableProps {
    items: RuleItem[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    onSelectionChange: (ids: Set<string>) => void;
    onEditRule: (item: RuleItem) => void;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
    rates: Map<string, RuleRate>;
    enabledCounterNames: Set<string>;
    onToggleCounter: (counterName: string) => void;
}

/** Virtualized ACL NG rule table using @tanstack/react-virtual. */
const RuleTable: React.FC<RuleTableProps> = ({
    items,
    selectedIds,
    activeRowId,
    onSelectionChange,
    onEditRule,
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
    rates,
    enabledCounterNames,
    onToggleCounter,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);
    const wrapRef = useRef<HTMLDivElement>(null);
    const bodyHeight = useContainerHeight(scrollRef, 300, FOOTER_HEIGHT + 20);
    const headerInnerRef = useRef<HTMLDivElement>(null);
    const hideTimeoutRef = useRef<number | null>(null);
    const [hoveredItem, setHoveredItem] = useState<RuleItem | null>(null);
    const [hoveredStart, setHoveredStart] = useState(0);
    const [bodyScrollTop, setBodyScrollTop] = useState(0);

    const rowVirtualizer = useVirtualizer({
        count: items.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    useEffect(() => {
        const el = scrollRef.current;
        if (!el) return;
        const onBodyScroll = (): void => {
            setBodyScrollTop(el.scrollTop);
            const inner = headerInnerRef.current;
            if (inner) {
                inner.style.transform = `translateX(-${el.scrollLeft}px)`;
            }
        };
        el.addEventListener('scroll', onBodyScroll, { passive: true });
        return () => el.removeEventListener('scroll', onBodyScroll);
    }, []);

    useEffect(() => () => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
        }
    }, []);

    const handleToggleSelect = useCallback((id: string): void => {
        const next = new Set(selectedIds);
        if (next.has(id)) next.delete(id);
        else next.add(id);
        onSelectionChange(next);
    }, [selectedIds, onSelectionChange]);

    const handleSelectAll = useCallback((_checked: boolean): void => {
        if (selectedIds.size === items.length && items.length > 0) {
            onSelectionChange(new Set());
        } else {
            onSelectionChange(new Set(items.map(item => item.id)));
        }
    }, [selectedIds.size, items, onSelectionChange]);

    const handleHoverChange = useCallback((item: RuleItem | null, start: number): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
        if (item === null) {
            hideTimeoutRef.current = window.setTimeout(() => {
                hideTimeoutRef.current = null;
                setHoveredItem(null);
            }, 80);
        } else {
            setHoveredItem(item);
            setHoveredStart(start);
        }
    }, []);

    const handleOverlayEdit = useCallback((): void => {
        if (hoveredItem) onEditRule(hoveredItem);
    }, [hoveredItem, onEditRule]);

    const handleOverlayMouseEnter = useCallback((): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
    }, []);

    const handleOverlayMouseLeave = useCallback((): void => {
        setHoveredItem(null);
    }, []);

    const isAllSelected = items.length > 0 && selectedIds.size === items.length;
    const isIndeterminate = selectedIds.size > 0 && selectedIds.size < items.length;
    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = useMemo(() => {
        if (items.length === 0 || virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${items.length.toLocaleString()}`;
    }, [virtualRows, items.length]);

    const overlayTopOffset = HEADER_HEIGHT + hoveredStart - bodyScrollTop;

    return (
        <div
            ref={wrapRef}
            className="fw-tbl-wrap acl-table"
        >
            <div className="fw-tbl-header-row">
                <div
                    className="fw-vtbl-header"
                    style={{ height: HEADER_HEIGHT }}
                >
                    <div ref={headerInnerRef} style={{ display: 'flex', minWidth: TOTAL_WIDTH, height: '100%', alignItems: 'center', willChange: 'transform' }}>
                    <div style={cellStyle('checkbox')}>
                        <Checkbox
                            indeterminate={isIndeterminate}
                            checked={isAllSelected}
                            onUpdate={handleSelectAll}
                            disabled={items.length === 0}
                            aria-label="Select all rules"
                        />
                    </div>
                    <div style={{ ...cellStyle('index'), justifyContent: 'center' }}>
                        <span className="fw-th-text">#</span>
                    </div>
                    <div style={cellStyle('srcs')}>
                        <span className="fw-th-text">Sources</span>
                    </div>
                    <div style={cellStyle('dsts')}>
                        <span className="fw-th-text">Destinations</span>
                    </div>
                    <div style={cellStyle('src_ports')}>
                        <span className="fw-th-text">Src ports</span>
                    </div>
                    <div style={cellStyle('dst_ports')}>
                        <span className="fw-th-text">Dst ports</span>
                    </div>
                    <div style={cellStyle('protos')}>
                        <span className="fw-th-text">Protocols</span>
                    </div>
                    <div style={cellStyle('vlans')}>
                        <span className="fw-th-text">VLANs</span>
                    </div>
                    <div style={cellStyle('devices')}>
                        <span className="fw-th-text">Devices</span>
                    </div>
                    <div style={cellStyle('counter')}>
                        <span className="fw-th-text">Counter</span>
                    </div>
                    <div style={cellStyle('sparkline')}>
                        <span className="fw-th-text">pps</span>
                    </div>
                    <div style={cellStyle('actions')}>
                        <span className="fw-th-text">Actions</span>
                    </div>
                    </div>
                </div>
                <DraftActionButtons
                    currentIsDirty={currentIsDirty}
                    onSave={onSave}
                    onDiscard={onDiscard}
                    onDeleteConfig={onDeleteConfig}
                />
            </div>

            <div
                ref={scrollRef}
                className="fw-vtbl-body"
                style={bodyHeight > 0 ? { flex: '0 0 auto', height: bodyHeight } : undefined}
            >
                {items.length === 0 ? (
                    <div className="fw-table-empty">No rules match your search.</div>
                ) : (
                    <div
                        style={{
                            height: rowVirtualizer.getTotalSize(),
                            minWidth: TOTAL_WIDTH,
                            position: 'relative',
                        }}
                    >
                        {virtualRows.map(virtualRow => {
                            const item = items[virtualRow.index];
                            if (!item) return null;
                            return (
                                <VirtualRow
                                    key={item.id}
                                    item={item}
                                    start={virtualRow.start}
                                    selected={selectedIds.has(item.id)}
                                    active={activeRowId === item.id}
                                    rate={rates.get(item.id)}
                                    counterEnabled={enabledCounterNames.has(effectiveCounterName(item.rule, item.index))}
                                    onToggleSelect={handleToggleSelect}
                                    onHoverChange={handleHoverChange}
                                    onToggleCounter={onToggleCounter}
                                />
                            );
                        })}
                    </div>
                )}
            </div>

            <div className="fw-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="fw-toolbar__count">{footerText}</span>
                {selectedIds.size > 0 && (
                    <span className="fw-toolbar__count" style={{ color: 'var(--fw-accent)' }}>
                        {selectedIds.size.toLocaleString()} selected
                    </span>
                )}
            </div>

            {hoveredItem !== null && (
                <div
                    className="fw-row-action-slot"
                    style={{ top: overlayTopOffset }}
                    onMouseEnter={handleOverlayMouseEnter}
                    onMouseLeave={handleOverlayMouseLeave}
                >
                    <button
                        type="button"
                        className="fw-row-edit-btn fw-row-edit-btn--visible"
                        onClick={handleOverlayEdit}
                        aria-label={`Edit rule ${hoveredItem.index + 1}`}
                        title="Edit rule"
                    >
                        ✎
                    </button>
                </div>
            )}
        </div>
    );
};

export default RuleTable;
