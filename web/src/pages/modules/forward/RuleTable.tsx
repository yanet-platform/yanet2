import React, { useCallback, useMemo, useRef, useState, useEffect, memo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox, Icon } from '@gravity-ui/uikit';
import { ArrowUturnCcwLeft } from '@gravity-ui/icons';
import type { RuleItem } from './types';
import type { RuleRate } from './useForwardRuleCounters';
import DirectionBadge from './DirectionBadge';
import AnyBadge from './AnyBadge';
import Sparkline from './Sparkline';
import { formatPps } from '../../../utils';

/** Save / floppy disk icon. */
const SaveIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 5h11l3 3v11H5zM8 5v5h7V5M8 14h8v5H8z" />
    </svg>
);

/** Trash / delete icon. */
const TrashIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 7h14M9 7V5h6v2M7 7l1 12h8l1-12" />
    </svg>
);

/** Duration in ms of the CSS transition on .fw-drawer — keep in sync with SCSS. */
const DRAWER_TRANSITION_MS = 220;
export { DRAWER_TRANSITION_MS };

const ROW_HEIGHT = 40;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

const COLUMN_WIDTHS = {
    checkbox: 38,
    index: 48,
    target: 160,
    mode: 90,
    counter: 140,
    devices: 140,
    vlans: 120,
    srcs: 200,
    dsts: 200,
    sparkline: 150,
} as const;


const TOTAL_WIDTH = Object.values(COLUMN_WIDTHS).reduce((a, b) => a + b, 0);

type ColKey = keyof typeof COLUMN_WIDTHS;

const cellStyle = (col: ColKey): React.CSSProperties => ({
    width: COLUMN_WIDTHS[col],
    minWidth: COLUMN_WIDTHS[col],
    maxWidth: COLUMN_WIDTHS[col],
    flexShrink: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    paddingRight: col === 'checkbox' ? 0 : 8,
    display: 'flex',
    alignItems: 'center',
    justifyContent: col === 'checkbox' || col === 'index' ? 'center' : 'flex-start',
});

/** Compact mono list of values with overflow truncation. */
const ValueCell: React.FC<{ values: string[] }> = ({ values }) => {
    if (values.length === 0) return null;
    const visible = values.slice(0, 3);
    const rest = values.length - visible.length;
    return (
        <span className="fw-cell-values" title={values.join(', ')}>
            {visible.map((v, idx) => (
                <span key={idx} className="fw-cell-mono">{v}</span>
            ))}
            {rest > 0 && <span className="fw-cell-more">+{rest}</span>}
        </span>
    );
};

interface VirtualRowProps {
    item: RuleItem;
    start: number;
    selected: boolean;
    active: boolean;
    rateData: RuleRate | null;
    onToggleSelect: (id: string) => void;
    onHoverChange: (item: RuleItem | null, start: number) => void;
}

/** Single virtualized rule row — no per-row action slot; hover is reported to the parent overlay. */
const VirtualRow: React.FC<VirtualRowProps> = memo(({
    item,
    start,
    selected,
    active,
    rateData,
    onToggleSelect,
    onHoverChange,
}) => {
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
    if (active) rowBg = 'var(--fw-accent-soft)';
    else if (selected) rowBg = 'var(--fw-accent-soft)';

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
            <div
                style={cellStyle('checkbox')}
                onClick={(e) => e.stopPropagation()}
            >
                <Checkbox
                    checked={selected}
                    onUpdate={handleCheckboxChange}
                    aria-label={`Select rule ${item.index + 1}`}
                />
            </div>

            <div style={{ ...cellStyle('index'), color: 'var(--fw-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                <span style={{ fontSize: 12 }}>{item.index + 1}</span>
            </div>

            <div style={cellStyle('target')} title={item.target}>
                <span className="fw-cell-mono fw-cell-strong">{item.target || '—'}</span>
            </div>

            <div style={cellStyle('mode')}>
                <DirectionBadge mode={item.mode} />
            </div>

            <div style={cellStyle('counter')} title={item.counter}>
                <span className="fw-cell-mono fw-cell-muted">{item.counter || '—'}</span>
            </div>

            <div style={cellStyle('devices')}>
                {item.deviceNames.length > 0
                    ? <ValueCell values={item.deviceNames} />
                    : <AnyBadge label="any" />
                }
            </div>

            <div style={cellStyle('vlans')}>
                {item.isAllVlans
                    ? <AnyBadge label="any" />
                    : <span className="fw-cell-mono fw-cell-muted">{item.vlansDisplay || '—'}</span>
                }
            </div>

            <div style={cellStyle('srcs')}>
                {item.isAnySrc
                    ? <AnyBadge label="any" />
                    : <ValueCell values={item.sourceCidrs} />
                }
            </div>

            <div style={cellStyle('dsts')}>
                {item.isAnyDst
                    ? <AnyBadge label="any" />
                    : <ValueCell values={item.dstCidrs} />
                }
            </div>

            <div style={{ ...cellStyle('sparkline'), gap: 8 }}>
                <Sparkline values={rateData?.history ?? null} width={56} height={16} />
                <span className="fw-cell-pps">
                    {rateData ? formatPps(rateData.pps) : '— pps'}
                </span>
            </div>
        </div>
    );
});

VirtualRow.displayName = 'VirtualRow';

interface RuleTableProps {
    items: RuleItem[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    /** Map from RuleItem.id to rate data (sparkline history + live pps). */
    rateValues: Map<string, RuleRate>;
    onSelectionChange: (ids: Set<string>) => void;
    onEditRule: (item: RuleItem) => void;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
}

/** Virtualized rule table using @tanstack/react-virtual. */
const RuleTable: React.FC<RuleTableProps> = ({
    items,
    selectedIds,
    activeRowId,
    rateValues,
    onSelectionChange,
    onEditRule,
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);

    /**
     * Pending hide timeout id. When the cursor leaves a row we schedule a
     * short delay before clearing hoveredItem, giving the overlay time to
     * receive its own mouseenter and cancel the hide.
     */
    const hideTimeoutRef = useRef<number | null>(null);

    /**
     * Hover state for the floating edit button overlay.
     * hoveredItem is null when no row is hovered.
     * hoveredStart is the virtualizer `start` offset (px from scroll content top).
     */
    const [hoveredItem, setHoveredItem] = useState<RuleItem | null>(null);
    const [hoveredStart, setHoveredStart] = useState(0);

    /**
     * Tracks the vertical scroll offset of the body so the overlay (which is
     * a child of .fw-tbl-wrap, not the scroll body) can compute its correct
     * top position: HEADER_HEIGHT + virtualizer_start - scrollTop.
     */
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
        const onScroll = (): void => setBodyScrollTop(el.scrollTop);
        el.addEventListener('scroll', onScroll, { passive: true });
        return () => el.removeEventListener('scroll', onScroll);
    }, []);

    // Cancel any pending hide timeout when the table unmounts.
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
            onSelectionChange(new Set(items.map((item) => item.id)));
        }
    }, [selectedIds.size, items, onSelectionChange]);

    const handleHoverChange = useCallback((item: RuleItem | null, start: number): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
        if (item === null) {
            // Schedule the hide so the overlay can intercept if the cursor
            // moved onto it rather than away from the table entirely.
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

    /**
     * When the cursor moves from the row into the overlay, cancel the pending
     * hide so the button stays mounted and clickable.
     */
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
        if (items.length === 0) return '';
        if (virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${items.length.toLocaleString()}`;
    }, [virtualRows, items.length]);

    /**
     * Edit-button overlay y-offset relative to .fw-tbl-wrap (position:relative).
     *
     * The overlay is a child of .fw-tbl-wrap, NOT the scroll body.  This means:
     *   • right: 0 resolves against .fw-tbl-wrap's right edge = wrap_right (fixed).
     *   • top must account for the header height and the current scroll position:
     *       top = HEADER_HEIGHT + virtualizer_start - scrollTop
     *
     * Geometry (button center vs header delete button center):
     *   header delete center  = wrap_right − 8px(padding-right) − 16px(half of 32px) = wrap_right − 24px
     *   slot width 40px, padding-right 8px, button 26px:
     *     button center from slot_right = (40 − 8 − 26) / 2 + 13 = 3 + 13 = 16px from content edge
     *     = 16 + 8 = 24px from slot_right = wrap_right − 24px  ✓
     *
     * Horizontal scrolling: the overlay is outside the scroll body so h-scroll
     * never moves it — it stays permanently at right: 0 of .fw-tbl-wrap.
     */
    const overlayTopOffset = HEADER_HEIGHT + hoveredStart - bodyScrollTop;

    return (
        <div className="fw-tbl-wrap">
            {/* Sticky header row */}
            <div className="fw-tbl-header-row">
                <div
                    className="fw-vtbl-header"
                    style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
                >
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
                    <div style={cellStyle('target')}>
                        <span className="fw-th-text">Target</span>
                    </div>
                    <div style={cellStyle('mode')}>
                        <span className="fw-th-text">Mode</span>
                    </div>
                    <div style={cellStyle('counter')}>
                        <span className="fw-th-text">Counter</span>
                    </div>
                    <div style={cellStyle('devices')}>
                        <span className="fw-th-text">Devices</span>
                    </div>
                    <div style={cellStyle('vlans')}>
                        <span className="fw-th-text">VLANs</span>
                    </div>
                    <div style={cellStyle('srcs')}>
                        <span className="fw-th-text">Sources</span>
                    </div>
                    <div style={cellStyle('dsts')}>
                        <span className="fw-th-text">Destinations</span>
                    </div>
                    <div style={cellStyle('sparkline')}>
                        <span className="fw-th-text">pps</span>
                    </div>
                </div>
                <div className="fw-tbl-actions">
                    {currentIsDirty && (
                        <button
                            type="button"
                            className="fw-tbl-action-btn fw-tbl-action-btn--discard"
                            title="Discard changes"
                            aria-label="Discard local changes"
                            onClick={onDiscard}
                        >
                            <Icon data={ArrowUturnCcwLeft} size={16} />
                        </button>
                    )}
                    <button
                        type="button"
                        className="fw-tbl-action-btn fw-tbl-action-btn--save"
                        title={currentIsDirty ? 'Review & apply' : 'No changes to save'}
                        aria-label="Review and apply changes"
                        disabled={!currentIsDirty}
                        onClick={onSave}
                    >
                        <SaveIcon />
                    </button>
                    <button
                        type="button"
                        className="fw-tbl-action-btn fw-tbl-action-btn--delete"
                        title="Delete config"
                        aria-label="Delete config"
                        onClick={onDeleteConfig}
                    >
                        <TrashIcon />
                    </button>
                </div>
            </div>

            {/* Virtualized scroll body */}
            <div
                ref={scrollRef}
                className="fw-vtbl-body"
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
                        {virtualRows.map((virtualRow) => {
                            const item = items[virtualRow.index];
                            if (!item) return null;
                            return (
                                <VirtualRow
                                    key={item.id}
                                    item={item}
                                    start={virtualRow.start}
                                    selected={selectedIds.has(item.id)}
                                    active={activeRowId === item.id}
                                    rateData={rateValues.get(item.id) ?? null}
                                    onToggleSelect={handleToggleSelect}
                                    onHoverChange={handleHoverChange}
                                />
                            );
                        })}
                    </div>
                )}

            </div>

            {/* Footer */}
            <div className="fw-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="fw-toolbar__count">{footerText}</span>
                {selectedIds.size > 0 && (
                    <span className="fw-toolbar__count" style={{ color: 'var(--fw-accent)' }}>
                        {selectedIds.size.toLocaleString()} selected
                    </span>
                )}
            </div>

            {/*
              * Floating edit button overlay — direct child of .fw-tbl-wrap
              * (position:relative), NOT inside the scroll body.
              *
              *  right: 0  → always at wrap_right, regardless of horizontal scroll.
              *  top       → HEADER_HEIGHT + virtualizer_start − scrollTop
              *              keeps the button vertically aligned with the hovered row
              *              while the body scrolls.
              *
              * Button center geometry (aligns with header delete button):
              *   slot: 40px wide, padding-right 8px, justify-content:center (32px effective)
              *   button: 26px centered in 32px → center offset from slot right = 8 + 3 + 13 = 24px
              *   wrap right − 24px  =  header delete button center  ✓
              *
              * The overlay is clipped by .fw-tbl-wrap (overflow:hidden) so it never
              * bleeds into the header or footer — rows at the very top/bottom that scroll
              * into the boundary just have the button disappear naturally.
              */}
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
                        aria-label={`Edit rule ${hoveredItem !== null ? hoveredItem.index + 1 : ''}`}
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
