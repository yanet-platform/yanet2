import React, { useCallback, useMemo, useRef, memo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox, Icon } from '@gravity-ui/uikit';
import { ArrowUturnCcwLeft } from '@gravity-ui/icons';
import type { RuleItem } from './types';
import DirectionBadge from './DirectionBadge';
import AnyBadge from './AnyBadge';
import Sparkline from './Sparkline';

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

/** Duration in ms of the CSS transition on .fwng-drawer — keep in sync with SCSS. */
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
    sparkline: 72,
    actions: 44,
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
    paddingRight: col === 'actions' || col === 'checkbox' ? 0 : 8,
    display: 'flex',
    alignItems: 'center',
    justifyContent: col === 'checkbox' || col === 'actions' || col === 'index' ? 'center' : 'flex-start',
});

/** Compact mono list of values with overflow truncation. */
const ValueCell: React.FC<{ values: string[] }> = ({ values }) => {
    if (values.length === 0) return null;
    const visible = values.slice(0, 3);
    const rest = values.length - visible.length;
    return (
        <span className="fwng-cell-values" title={values.join(', ')}>
            {visible.map((v, idx) => (
                <span key={idx} className="fwng-cell-mono">{v}</span>
            ))}
            {rest > 0 && <span className="fwng-cell-more">+{rest}</span>}
        </span>
    );
};

interface VirtualRowProps {
    item: RuleItem;
    start: number;
    selected: boolean;
    active: boolean;
    sparklineValues: number[] | null;
    onToggleSelect: (id: string) => void;
    onEdit: (item: RuleItem) => void;
}

/** Single virtualized rule row. */
const VirtualRow: React.FC<VirtualRowProps> = memo(({
    item,
    start,
    selected,
    active,
    sparklineValues,
    onToggleSelect,
    onEdit,
}) => {
    const handleCheckboxChange = useCallback((_checked: boolean): void => {
        onToggleSelect(item.id);
    }, [onToggleSelect, item.id]);

    const handleEditClick = useCallback((): void => {
        onEdit(item);
    }, [onEdit, item]);

    let rowBg = 'transparent';
    if (active) rowBg = 'var(--fwng-accent-soft)';
    else if (selected) rowBg = 'var(--fwng-accent-soft)';

    return (
        <div
            className={`fwng-vrow${selected ? ' fwng-vrow--selected' : ''}${active ? ' fwng-vrow--active' : ''}`}
            style={{
                position: 'absolute',
                top: start,
                left: 0,
                height: ROW_HEIGHT,
                minWidth: TOTAL_WIDTH,
                width: '100%',
                display: 'flex',
                alignItems: 'center',
                borderBottom: '1px solid var(--fwng-line)',
                backgroundColor: rowBg,
                paddingLeft: 4,
                paddingRight: 4,
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

            <div style={{ ...cellStyle('index'), color: 'var(--fwng-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                <span style={{ fontSize: 12 }}>{item.index + 1}</span>
            </div>

            <div style={cellStyle('target')} title={item.target}>
                <span className="fwng-cell-mono fwng-cell-strong">{item.target || '—'}</span>
            </div>

            <div style={cellStyle('mode')}>
                <DirectionBadge mode={item.mode} />
            </div>

            <div style={cellStyle('counter')} title={item.counter}>
                <span className="fwng-cell-mono fwng-cell-muted">{item.counter || '—'}</span>
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
                    : <span className="fwng-cell-mono fwng-cell-muted">{item.vlansDisplay || '—'}</span>
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

            <div style={cellStyle('sparkline')}>
                <Sparkline values={sparklineValues} width={56} height={16} />
            </div>

            <div style={cellStyle('actions')}>
                <button
                    type="button"
                    className="fwng-row-edit-btn"
                    onClick={handleEditClick}
                    aria-label={`Edit rule ${item.index + 1}`}
                    title="Edit rule"
                >
                    ✎
                </button>
            </div>
        </div>
    );
});

VirtualRow.displayName = 'VirtualRow';

interface RuleTableProps {
    items: RuleItem[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    /** Map from RuleItem.id to pps sparkline history (60 samples). */
    counterValues: Map<string, number[]>;
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
    counterValues,
    onSelectionChange,
    onEditRule,
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);

    const rowVirtualizer = useVirtualizer({
        count: items.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

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

    return (
        <div className="fwng-tbl-wrap">
            {/* Sticky header row */}
            <div className="fwng-tbl-header-row">
                <div
                    className="fwng-vtbl-header"
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
                        <span className="fwng-th-text">#</span>
                    </div>
                    <div style={cellStyle('target')}>
                        <span className="fwng-th-text">Target</span>
                    </div>
                    <div style={cellStyle('mode')}>
                        <span className="fwng-th-text">Mode</span>
                    </div>
                    <div style={cellStyle('counter')}>
                        <span className="fwng-th-text">Counter</span>
                    </div>
                    <div style={cellStyle('devices')}>
                        <span className="fwng-th-text">Devices</span>
                    </div>
                    <div style={cellStyle('vlans')}>
                        <span className="fwng-th-text">VLANs</span>
                    </div>
                    <div style={cellStyle('srcs')}>
                        <span className="fwng-th-text">Sources</span>
                    </div>
                    <div style={cellStyle('dsts')}>
                        <span className="fwng-th-text">Destinations</span>
                    </div>
                    <div style={cellStyle('sparkline')}>
                        <span className="fwng-th-text">pps</span>
                    </div>
                    <div style={cellStyle('actions')} />
                </div>
                <div className="fwng-tbl-actions">
                    {currentIsDirty && (
                        <button
                            type="button"
                            className="fwng-tbl-action-btn fwng-tbl-action-btn--discard"
                            title="Discard changes"
                            aria-label="Discard local changes"
                            onClick={onDiscard}
                        >
                            <Icon data={ArrowUturnCcwLeft} size={16} />
                        </button>
                    )}
                    <button
                        type="button"
                        className="fwng-tbl-action-btn fwng-tbl-action-btn--save"
                        title={currentIsDirty ? 'Review & apply' : 'No changes to save'}
                        aria-label="Review and apply changes"
                        disabled={!currentIsDirty}
                        onClick={onSave}
                    >
                        <SaveIcon />
                    </button>
                    <button
                        type="button"
                        className="fwng-tbl-action-btn fwng-tbl-action-btn--delete"
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
                className="fwng-vtbl-body"
            >
                {items.length === 0 ? (
                    <div className="fwng-table-empty">No rules match your search.</div>
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
                                    sparklineValues={counterValues.get(item.id) ?? null}
                                    onToggleSelect={handleToggleSelect}
                                    onEdit={onEditRule}
                                />
                            );
                        })}
                    </div>
                )}
            </div>

            {/* Footer */}
            <div className="fwng-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="fwng-toolbar__count">{footerText}</span>
                {selectedIds.size > 0 && (
                    <span className="fwng-toolbar__count" style={{ color: 'var(--fwng-accent)' }}>
                        {selectedIds.size.toLocaleString()} selected
                    </span>
                )}
            </div>
        </div>
    );
};

export default RuleTable;
