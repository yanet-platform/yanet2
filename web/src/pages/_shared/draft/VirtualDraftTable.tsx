import React, { useCallback, useMemo, useRef, memo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox } from '@gravity-ui/uikit';
import DraftActionButtons from './DraftActionButtons';
import { useRowHoverOverlay } from './useRowHoverOverlay';
import RemovedRowsSection from './RemovedRowsSection';
import type { RemovedColumnDescriptor } from './RemovedRowsSection';

const ROW_HEIGHT = 40;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

/** Width constants for the four leading structural cells. */
export const LEADING_CELL_WIDTHS = {
    checkbox: 38,
    handle: 32,
    index: 48,
    status: 24,
} as const;

/** Sum of all four leading cell widths, for computing total table width in consumers. */
export const LEADING_TOTAL_WIDTH =
    LEADING_CELL_WIDTHS.checkbox +
    LEADING_CELL_WIDTHS.handle +
    LEADING_CELL_WIDTHS.index +
    LEADING_CELL_WIDTHS.status;

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

/** Column descriptor for data (non-leading) header cells. */
export interface TableColumnHeader {
    /** Fixed pixel width. */
    width: number;
    /** Text label shown in the header. */
    label: string;
}

/** Status dot types emitted by a row. */
export type RowStatus = 'added' | 'changed' | 'same';

/** Per-row rendering callback for data cells. */
export type RenderDataCells<T> = (row: T) => React.ReactNode;

interface VirtualRowShellProps<T extends { id: string }> {
    row: T;
    realIndex: number;
    start: number;
    status: RowStatus | undefined;
    active: boolean;
    editing: boolean;
    selected: boolean;
    dragOver: 'top' | 'bottom' | null;
    totalWidth: number;
    onHoverChange: (row: T | null, start: number) => void;
    onDragStart: (e: React.DragEvent) => void;
    onDragOver: (e: React.DragEvent) => void;
    onDragLeave: () => void;
    onDrop: (e: React.DragEvent) => void;
    onCheckboxChange: (checked: boolean) => void;
    renderDataCells: RenderDataCells<T>;
}

/** Generic virtualized row shell — leading cells + pluggable data cells. */
const VirtualRowShell = memo(<T extends { id: string }>({
    row,
    realIndex,
    start,
    status,
    active,
    editing,
    selected,
    dragOver,
    totalWidth,
    onHoverChange,
    onDragStart,
    onDragOver,
    onDragLeave,
    onDrop,
    onCheckboxChange,
    renderDataCells,
}: VirtualRowShellProps<T>) => {
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

    return (
        <div
            className={`fw-vrow${active ? ' fw-vrow--active' : ''}${dragCls}${selected ? ' fw-vrow--selected' : ''}`}
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
                minWidth: totalWidth,
                width: '100%',
                display: 'flex',
                alignItems: 'center',
                borderBottom: '1px solid var(--fw-line)',
                backgroundColor: rowBg,
                paddingLeft: 4,
            }}
        >
            <div
                style={{ width: LEADING_CELL_WIDTHS.checkbox, minWidth: LEADING_CELL_WIDTHS.checkbox, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                onClick={(e) => e.stopPropagation()}
            >
                <Checkbox checked={selected} onUpdate={onCheckboxChange} size="m" />
            </div>

            <div
                style={{ width: LEADING_CELL_WIDTHS.handle, minWidth: LEADING_CELL_WIDTHS.handle, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                onClick={(e) => e.stopPropagation()}
            >
                <div className="fw-drag-handle" draggable onDragStart={onDragStart} title="Drag to reorder">
                    {DRAG_ICON}
                </div>
            </div>

            <div style={{ width: LEADING_CELL_WIDTHS.index, minWidth: LEADING_CELL_WIDTHS.index, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--fw-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                <span style={{ fontSize: 12 }}>{realIndex + 1}</span>
            </div>

            <div style={{ width: LEADING_CELL_WIDTHS.status, minWidth: LEADING_CELL_WIDTHS.status, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                {status === 'added' && <span className="fw-status-dot fw-status-dot--added" title="Added (not yet committed)" />}
                {status === 'changed' && <span className="fw-status-dot fw-status-dot--changed" title="Modified (not yet committed)" />}
            </div>

            {renderDataCells(row)}
        </div>
    );
}) as <T extends { id: string }>(props: VirtualRowShellProps<T>) => React.JSX.Element;

(VirtualRowShell as React.FC).displayName = 'VirtualRowShell';

export interface VirtualDraftTableProps<T extends { id: string }> {
    allRows: T[];
    visibleRows: T[];
    statusById: Map<string, RowStatus>;
    removedRows: T[];
    activeRowId: string | null;
    editingRowId: string | null;
    selectedIds: Set<string>;
    dragOverState: { id: string | null; where: 'top' | 'bottom' | null };
    onRowClick: (id: string) => void;
    onEditRow: (id: string) => void;
    onRestoreRow: (row: T) => void;
    onSelectionChange: (ids: Set<string>) => void;
    onDragStart: (id: string, e: React.DragEvent) => void;
    onDragOver: (id: string, e: React.DragEvent) => void;
    onDragLeave: () => void;
    onDrop: (id: string, e: React.DragEvent) => void;
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
    /** Total pixel width of all columns combined. */
    totalWidth: number;
    /** Data column header descriptors (right of the 4 fixed leading cells). */
    columnHeaders: TableColumnHeader[];
    /** Render the data cells for a live row. */
    renderDataCells: RenderDataCells<T>;
    /** Columns used in the removed-rows ghost section. */
    removedColumns: RemovedColumnDescriptor<T>[];
    /** Noun shown in the hover edit button aria-label and title, e.g. "route". */
    itemNoun: string;
    /** Message shown when visibleRows is empty. */
    emptyMessage: string;
}

/** Generic virtualized draft table. Used by FIBTable and PrefixTable. */
export const VirtualDraftTable = <T extends { id: string }>({
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
    totalWidth,
    columnHeaders,
    renderDataCells,
    removedColumns,
    itemNoun,
    emptyMessage,
}: VirtualDraftTableProps<T>): React.JSX.Element => {
    const scrollRef = useRef<HTMLDivElement>(null);

    const {
        hoveredRow,
        overlayTopOffset,
        handleHoverChange,
        handleOverlayMouseEnter,
        handleOverlayMouseLeave,
        attachScrollEl,
    } = useRowHoverOverlay<T>(HEADER_HEIGHT);

    const setScrollRef = useCallback((el: HTMLDivElement | null): void => {
        (scrollRef as React.MutableRefObject<HTMLDivElement | null>).current = el;
        attachScrollEl(el);
    }, [attachScrollEl]);

    const rowVirtualizer = useVirtualizer({
        count: visibleRows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    const handleOverlayEdit = useCallback((): void => {
        if (hoveredRow) {
            onRowClick(hoveredRow.id);
            onEditRow(hoveredRow.id);
        }
    }, [hoveredRow, onRowClick, onEditRow]);

    const isAllSelected = visibleRows.length > 0 && visibleRows.every((r) => selectedIds.has(r.id));
    const isIndeterminate = !isAllSelected && visibleRows.some((r) => selectedIds.has(r.id));

    const handleSelectAll = useCallback((checked: boolean): void => {
        onSelectionChange(checked ? new Set(visibleRows.map((r) => r.id)) : new Set());
    }, [visibleRows, onSelectionChange]);

    const handleRowCheckboxChange = useCallback((rowId: string, checked: boolean): void => {
        const next = new Set(selectedIds);
        if (checked) next.add(rowId); else next.delete(rowId);
        onSelectionChange(next);
    }, [selectedIds, onSelectionChange]);

    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = useMemo(() => {
        if (visibleRows.length === 0 || virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${visibleRows.length.toLocaleString()}`;
    }, [virtualRows, visibleRows.length]);

    return (
        <div className="fw-tbl-wrap">
            <div className="fw-tbl-header-row">
                <div className="fw-vtbl-header" style={{ height: HEADER_HEIGHT, minWidth: totalWidth }}>
                    <div
                        style={{ width: LEADING_CELL_WIDTHS.checkbox, minWidth: LEADING_CELL_WIDTHS.checkbox, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
                    </div>
                    <div style={{ width: LEADING_CELL_WIDTHS.handle, minWidth: LEADING_CELL_WIDTHS.handle, flexShrink: 0 }} />
                    <div style={{ width: LEADING_CELL_WIDTHS.index, minWidth: LEADING_CELL_WIDTHS.index, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <span className="fw-th-text">#</span>
                    </div>
                    <div style={{ width: LEADING_CELL_WIDTHS.status, minWidth: LEADING_CELL_WIDTHS.status, flexShrink: 0 }} />
                    {columnHeaders.map((col) => (
                        <div key={col.label} style={{ width: col.width, minWidth: col.width, flexShrink: 0, display: 'flex', alignItems: 'center', paddingRight: 8 }}>
                            <span className="fw-th-text">{col.label}</span>
                        </div>
                    ))}
                </div>
                <DraftActionButtons
                    currentIsDirty={currentIsDirty}
                    onSave={onSave}
                    onDiscard={onDiscard}
                    onDeleteConfig={onDeleteConfig}
                />
            </div>

            <div ref={setScrollRef} className="fw-vtbl-body">
                {visibleRows.length === 0 ? (
                    <div className="fw-table-empty">{emptyMessage}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth: totalWidth, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const row = visibleRows[virtualRow.index];
                            if (!row) return null;
                            const realIdx = allRows.findIndex((r) => r.id === row.id);
                            return (
                                <VirtualRowShell
                                    key={row.id}
                                    row={row}
                                    realIndex={realIdx}
                                    start={virtualRow.start}
                                    status={statusById.get(row.id)}
                                    active={activeRowId === row.id}
                                    editing={editingRowId === row.id}
                                    selected={selectedIds.has(row.id)}
                                    dragOver={dragOverState.id === row.id ? dragOverState.where : null}
                                    totalWidth={totalWidth}
                                    onHoverChange={handleHoverChange}
                                    onDragStart={(e) => onDragStart(row.id, e)}
                                    onDragOver={(e) => onDragOver(row.id, e)}
                                    onDragLeave={onDragLeave}
                                    onDrop={(e) => onDrop(row.id, e)}
                                    onCheckboxChange={(checked) => handleRowCheckboxChange(row.id, checked)}
                                    renderDataCells={renderDataCells}
                                />
                            );
                        })}
                    </div>
                )}

                <RemovedRowsSection
                    rows={removedRows}
                    rowHeight={ROW_HEIGHT}
                    totalWidth={totalWidth}
                    leadingWidths={LEADING_CELL_WIDTHS}
                    columns={removedColumns}
                    onRestore={onRestoreRow}
                />
            </div>

            <div className="fw-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="fw-toolbar__count">{footerText}</span>
            </div>

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
                        aria-label={`Edit ${itemNoun} ${allRows.findIndex((r) => r.id === hoveredRow.id) + 1}`}
                        title={`Edit ${itemNoun}`}
                    >
                        ✎
                    </button>
                </div>
            )}
        </div>
    );
};
