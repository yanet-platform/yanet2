import React, { useCallback, useMemo, useRef, useState, useEffect, memo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox, Icon } from '@gravity-ui/uikit';
import { ArrowUturnCcwLeft } from '@gravity-ui/icons';
import type { FIBRowItem, FIBRowStatus } from './types';
import { validateRow } from './validation';

const ROW_HEIGHT = 40;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

const COLUMN_WIDTHS = {
    checkbox: 38,
    handle: 32,
    index: 48,
    status: 24,
    prefix: 220,
    dst_mac: 180,
    src_mac: 180,
    device: 120,
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
    paddingRight: col === 'checkbox' || col === 'handle' || col === 'status' ? 0 : 8,
    display: 'flex',
    alignItems: 'center',
    justifyContent: col === 'checkbox' || col === 'handle' || col === 'index' || col === 'status' ? 'center' : 'flex-start',
});

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

const DRAG_ICON = (
    <svg width="14" height="14" viewBox="0 0 14 14" fill="none" aria-hidden="true">
        <rect x="3" y="3" width="2" height="2" rx="1" fill="currentColor" />
        <rect x="3" y="6" width="2" height="2" rx="1" fill="currentColor" />
        <rect x="3" y="9" width="2" height="2" rx="1" fill="currentColor" />
        <rect x="7" y="3" width="2" height="2" rx="1" fill="currentColor" />
        <rect x="7" y="6" width="2" height="2" rx="1" fill="currentColor" />
        <rect x="7" y="9" width="2" height="2" rx="1" fill="currentColor" />
    </svg>
);

const RESTORE_ICON = (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
        <path d="M3 3v5h5" />
    </svg>
);

interface VirtualFIBRowProps {
    row: FIBRowItem;
    realIndex: number;
    start: number;
    status: FIBRowStatus | undefined;
    active: boolean;
    editing: boolean;
    selected: boolean;
    dragOver: 'top' | 'bottom' | null;
    onHoverChange: (row: FIBRowItem | null, start: number) => void;
    onDragStart: (e: React.DragEvent) => void;
    onDragOver: (e: React.DragEvent) => void;
    onDragLeave: () => void;
    onDrop: (e: React.DragEvent) => void;
    onCheckboxChange: (checked: boolean) => void;
}

/** Single virtualized FIB row. */
const VirtualFIBRow: React.FC<VirtualFIBRowProps> = memo(({
    row,
    realIndex,
    start,
    status,
    active,
    editing,
    selected,
    dragOver,
    onHoverChange,
    onDragStart,
    onDragOver,
    onDragLeave,
    onDrop,
    onCheckboxChange,
}) => {
    const errors = useMemo(() => validateRow(row), [row]);

    const handleMouseEnter = useCallback((): void => {
        onHoverChange(row, start);
    }, [onHoverChange, row, start]);

    const handleMouseLeave = useCallback((): void => {
        onHoverChange(null, 0);
    }, [onHoverChange]);

    let rowBg = 'transparent';
    if (selected) rowBg = 'var(--fw-accent-soft)';
    else if (active || editing) rowBg = 'var(--fw-accent-soft)';

    const dragCls = dragOver === 'top'
        ? ' fw-vrow--drag-top'
        : dragOver === 'bottom'
            ? ' fw-vrow--drag-bottom'
            : '';

    const selectedCls = selected ? ' fw-vrow--selected' : '';

    return (
        <div
            className={`fw-vrow${active ? ' fw-vrow--active' : ''}${dragCls}${selectedCls}`}
            data-row-id={row.id}
            onMouseEnter={handleMouseEnter}
            onMouseLeave={handleMouseLeave}
            onDragOver={onDragOver}
            onDragLeave={onDragLeave}
            onDrop={onDrop}
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
                    onUpdate={onCheckboxChange}
                    size="m"
                />
            </div>

            <div
                style={cellStyle('handle')}
                onClick={(e) => e.stopPropagation()}
            >
                <div
                    className="fw-drag-handle"
                    draggable
                    onDragStart={onDragStart}
                    title="Drag to reorder"
                >
                    {DRAG_ICON}
                </div>
            </div>

            <div style={{ ...cellStyle('index'), color: 'var(--fw-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                <span style={{ fontSize: 12 }}>{realIndex + 1}</span>
            </div>

            <div style={cellStyle('status')}>
                {status === 'added' && <span className="fw-status-dot fw-status-dot--added" title="Added (not yet committed)" />}
                {status === 'changed' && <span className="fw-status-dot fw-status-dot--changed" title="Modified (not yet committed)" />}
            </div>

            <div
                style={{
                    ...cellStyle('prefix'),
                    ...(errors.prefix ? { color: 'var(--fw-danger)' } : {}),
                }}
                title={row.prefix || undefined}
            >
                <span className="fw-cell-mono fw-cell-strong">{row.prefix || <span style={{ color: 'var(--fw-text-3)', fontStyle: 'italic' }}>prefix?</span>}</span>
            </div>

            <div
                style={{
                    ...cellStyle('dst_mac'),
                    ...(errors.dst_mac ? { color: 'var(--fw-danger)' } : {}),
                }}
                title={row.dst_mac || undefined}
            >
                <span className="fw-cell-mono fw-cell-muted">{row.dst_mac || '—'}</span>
            </div>

            <div
                style={{
                    ...cellStyle('src_mac'),
                    ...(errors.src_mac ? { color: 'var(--fw-danger)' } : {}),
                }}
                title={row.src_mac || undefined}
            >
                <span className="fw-cell-mono fw-cell-muted">{row.src_mac || '—'}</span>
            </div>

            <div
                style={{
                    ...cellStyle('device'),
                    ...(errors.device ? { color: 'var(--fw-danger)' } : {}),
                }}
                title={row.device || undefined}
            >
                <span className="fw-cell-mono fw-cell-muted">{row.device || '—'}</span>
            </div>
        </div>
    );
});

VirtualFIBRow.displayName = 'VirtualFIBRow';

/** Ghost section showing server-only rows removed from draft. Non-virtualized (typically small). */
const RemovedRows: React.FC<{
    rows: FIBRowItem[];
    onRestore: (row: FIBRowItem) => void;
}> = ({ rows, onRestore }) => {
    if (rows.length === 0) return null;
    return (
        <div style={{ minWidth: TOTAL_WIDTH }}>
            {rows.map((r) => (
                <div
                    key={r.id}
                    className="fw-vrow"
                    style={{
                        display: 'flex',
                        alignItems: 'center',
                        height: ROW_HEIGHT,
                        minWidth: TOTAL_WIDTH,
                        borderBottom: '1px solid var(--fw-line)',
                        paddingLeft: 4,
                        opacity: 0.55,
                        background: 'rgba(224, 122, 110, 0.04)',
                        position: 'relative',
                    }}
                >
                    <div style={cellStyle('checkbox')} />
                    <div style={cellStyle('handle')}>
                        <span style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--fw-danger)', fontSize: 11 }}>✕</span>
                    </div>
                    <div style={{ ...cellStyle('index'), color: 'var(--fw-text-3)' }}>
                        <span style={{ fontSize: 12 }}>—</span>
                    </div>
                    <div style={cellStyle('status')}>
                        <span className="fw-status-dot fw-status-dot--removed" title="Removed in draft" />
                    </div>
                    <div style={cellStyle('prefix')}>
                        <span className="fw-cell-mono" style={{ textDecoration: 'line-through', textDecorationColor: 'var(--fw-danger)' }}>{r.prefix}</span>
                    </div>
                    <div style={cellStyle('dst_mac')}>
                        <span className="fw-cell-mono fw-cell-muted" style={{ textDecoration: 'line-through', textDecorationColor: 'var(--fw-danger)' }}>{r.dst_mac}</span>
                    </div>
                    <div style={cellStyle('src_mac')}>
                        <span className="fw-cell-mono fw-cell-muted" style={{ textDecoration: 'line-through', textDecorationColor: 'var(--fw-danger)' }}>{r.src_mac}</span>
                    </div>
                    <div style={cellStyle('device')}>
                        <span className="fw-cell-mono fw-cell-muted" style={{ textDecoration: 'line-through', textDecorationColor: 'var(--fw-danger)' }}>{r.device}</span>
                    </div>
                    <div style={{ marginLeft: 'auto', paddingRight: 8 }}>
                        <button
                            type="button"
                            className="fw-row-edit-btn fw-row-edit-btn--visible"
                            onClick={() => onRestore(r)}
                            title="Restore row"
                            aria-label="Restore removed row"
                            style={{ fontSize: 12 }}
                        >
                            {RESTORE_ICON}
                        </button>
                    </div>
                </div>
            ))}
        </div>
    );
};

export interface FIBTableProps {
    /** All draft rows for the config (filtered rows are passed separately). */
    allRows: FIBRowItem[];
    /** Subset of allRows after search filter (display only — indices reference allRows). */
    visibleRows: FIBRowItem[];
    /** Map from row id to status vs server. */
    statusById: Map<string, FIBRowStatus>;
    /** Server-only rows that are not in the draft (shown as removed ghost section). */
    removedRows: FIBRowItem[];
    /** Currently selected row id (keyboard nav). */
    activeRowId: string | null;
    /** Currently edited row id (drawer open). */
    editingRowId: string | null;
    /** Selected row ids for bulk actions. */
    selectedIds: Set<string>;
    /** Drag over indicator state. */
    dragOverState: { id: string | null; where: 'top' | 'bottom' | null };
    onRowClick: (id: string) => void;
    onEditRow: (id: string) => void;
    onRestoreRow: (row: FIBRowItem) => void;
    onSelectionChange: (ids: Set<string>) => void;
    onDragStart: (id: string, e: React.DragEvent) => void;
    onDragOver: (id: string, e: React.DragEvent) => void;
    onDragLeave: () => void;
    onDrop: (id: string, e: React.DragEvent) => void;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
}

/** Virtualized FIB table — structural 1:1 mirror of Forward's RuleTable. */
export const FIBTable: React.FC<FIBTableProps> = ({
    allRows,
    visibleRows,
    statusById,
    removedRows,
    activeRowId,
    editingRowId,
    selectedIds,
    dragOverState,
    onRowClick,
    onEditRow,
    onRestoreRow,
    onSelectionChange,
    onDragStart,
    onDragOver,
    onDragLeave,
    onDrop,
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);

    /**
     * Pending hide timeout id. When the cursor leaves a row we schedule a
     * short delay before clearing hoveredRow, giving the overlay time to
     * receive its own mouseenter and cancel the hide.
     */
    const hideTimeoutRef = useRef<number | null>(null);

    /**
     * Hover state for the floating action button overlay.
     * hoveredRow is null when no row is hovered.
     * hoveredStart is the virtualizer `start` offset (px from scroll content top).
     */
    const [hoveredRow, setHoveredRow] = useState<FIBRowItem | null>(null);
    const [hoveredStart, setHoveredStart] = useState(0);

    /**
     * Tracks the vertical scroll offset of the body so the overlay (which is
     * a child of .fw-tbl-wrap, not the scroll body) can compute its correct
     * top position: HEADER_HEIGHT + virtualizer_start - scrollTop.
     */
    const [bodyScrollTop, setBodyScrollTop] = useState(0);

    const rowVirtualizer = useVirtualizer({
        count: visibleRows.length,
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

    useEffect(() => () => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
        }
    }, []);

    const handleHoverChange = useCallback((row: FIBRowItem | null, start: number): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
        if (row === null) {
            hideTimeoutRef.current = window.setTimeout(() => {
                hideTimeoutRef.current = null;
                setHoveredRow(null);
            }, 80);
        } else {
            setHoveredRow(row);
            setHoveredStart(start);
        }
    }, []);

    const handleOverlayEdit = useCallback((): void => {
        if (hoveredRow) {
            onRowClick(hoveredRow.id);
            onEditRow(hoveredRow.id);
        }
    }, [hoveredRow, onRowClick, onEditRow]);

    /**
     * When the cursor moves from the row into the overlay, cancel the pending
     * hide so the buttons stay mounted and clickable.
     */
    const handleOverlayMouseEnter = useCallback((): void => {
        if (hideTimeoutRef.current !== null) {
            window.clearTimeout(hideTimeoutRef.current);
            hideTimeoutRef.current = null;
        }
    }, []);

    const handleOverlayMouseLeave = useCallback((): void => {
        setHoveredRow(null);
    }, []);

    const isAllSelected = visibleRows.length > 0 && visibleRows.every((r) => selectedIds.has(r.id));
    const isIndeterminate = !isAllSelected && visibleRows.some((r) => selectedIds.has(r.id));

    const handleSelectAll = useCallback((checked: boolean): void => {
        if (checked) {
            onSelectionChange(new Set(visibleRows.map((r) => r.id)));
        } else {
            onSelectionChange(new Set());
        }
    }, [visibleRows, onSelectionChange]);

    const handleRowCheckboxChange = useCallback((rowId: string, checked: boolean): void => {
        const next = new Set(selectedIds);
        if (checked) {
            next.add(rowId);
        } else {
            next.delete(rowId);
        }
        onSelectionChange(next);
    }, [selectedIds, onSelectionChange]);

    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = useMemo(() => {
        if (visibleRows.length === 0) return '';
        if (virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${visibleRows.length.toLocaleString()}`;
    }, [virtualRows, visibleRows.length]);

    /**
     * Edit-button overlay y-offset relative to .fw-tbl-wrap (position:relative).
     * top = HEADER_HEIGHT + virtualizer_start - scrollTop
     */
    const overlayTopOffset = HEADER_HEIGHT + hoveredStart - bodyScrollTop;

    return (
        <div className="fw-tbl-wrap">
            <div className="fw-tbl-header-row">
                <div
                    className="fw-vtbl-header"
                    style={{ height: HEADER_HEIGHT, minWidth: TOTAL_WIDTH }}
                >
                    <div style={cellStyle('checkbox')} onClick={(e) => e.stopPropagation()}>
                        <Checkbox
                            checked={isAllSelected}
                            indeterminate={isIndeterminate}
                            onUpdate={handleSelectAll}
                            size="m"
                        />
                    </div>
                    <div style={cellStyle('handle')} />
                    <div style={{ ...cellStyle('index'), justifyContent: 'center' }}>
                        <span className="fw-th-text">#</span>
                    </div>
                    <div style={cellStyle('status')} />
                    <div style={cellStyle('prefix')}>
                        <span className="fw-th-text">Prefix</span>
                    </div>
                    <div style={cellStyle('dst_mac')}>
                        <span className="fw-th-text">Dst MAC</span>
                    </div>
                    <div style={cellStyle('src_mac')}>
                        <span className="fw-th-text">Src MAC</span>
                    </div>
                    <div style={cellStyle('device')}>
                        <span className="fw-th-text">Device</span>
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

            <div
                ref={scrollRef}
                className="fw-vtbl-body"
            >
                {visibleRows.length === 0 ? (
                    <div className="fw-table-empty">No routes match your search.</div>
                ) : (
                    <div
                        style={{
                            height: rowVirtualizer.getTotalSize(),
                            minWidth: TOTAL_WIDTH,
                            position: 'relative',
                        }}
                    >
                        {virtualRows.map((virtualRow) => {
                            const row = visibleRows[virtualRow.index];
                            if (!row) return null;
                            const realIdx = allRows.findIndex((r) => r.id === row.id);
                            return (
                                <VirtualFIBRow
                                    key={row.id}
                                    row={row}
                                    realIndex={realIdx}
                                    start={virtualRow.start}
                                    status={statusById.get(row.id)}
                                    active={activeRowId === row.id}
                                    editing={editingRowId === row.id}
                                    selected={selectedIds.has(row.id)}
                                    dragOver={dragOverState.id === row.id ? dragOverState.where : null}
                                    onHoverChange={handleHoverChange}
                                    onDragStart={(e) => onDragStart(row.id, e)}
                                    onDragOver={(e) => onDragOver(row.id, e)}
                                    onDragLeave={onDragLeave}
                                    onDrop={(e) => onDrop(row.id, e)}
                                    onCheckboxChange={(checked) => handleRowCheckboxChange(row.id, checked)}
                                />
                            );
                        })}
                    </div>
                )}

                {removedRows.length > 0 && (
                    <RemovedRows rows={removedRows} onRestore={onRestoreRow} />
                )}
            </div>

            <div className="fw-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="fw-toolbar__count">{footerText}</span>
            </div>

            {/*
              * Floating edit-button overlay — direct child of .fw-tbl-wrap
              * (position:relative), NOT inside the scroll body.
              *
              *  right: 0  → always at wrap_right, regardless of horizontal scroll.
              *  top       → HEADER_HEIGHT + virtualizer_start − scrollTop
              *              keeps the buttons vertically aligned with the hovered row
              *              while the body scrolls vertically.
              *
              * The overlay is clipped by .fw-tbl-wrap (overflow:hidden) so buttons
              * disappear naturally when the row scrolls out of view.
              */}
            {hoveredRow !== null && (
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
                        aria-label={`Edit route ${hoveredRow !== null ? (allRows.findIndex((r) => r.id === hoveredRow.id) + 1) : ''}`}
                        title="Edit route"
                    >
                        ✎
                    </button>
                </div>
            )}
        </div>
    );
};
