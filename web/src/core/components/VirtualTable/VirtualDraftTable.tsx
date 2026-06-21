import React, { useCallback, useMemo, useRef, memo } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox } from '@gravity-ui/uikit';
import { useContainerHeight } from '../../hooks/useContainerHeight';
import RemovedRowsSection from './RemovedRowsSection';
import type { RemovedColumnDescriptor } from './RemovedRowsSection';

const ROW_HEIGHT = 44;
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
    onClick: () => void;
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
    onClick,
    onDragStart,
    onDragOver,
    onDragLeave,
    onDrop,
    onCheckboxChange,
    renderDataCells,
}: VirtualRowShellProps<T>) => {
    let rowBg = 'transparent';
    if (selected) rowBg = 'var(--yn-accent-soft)';
    else if (active || editing) rowBg = 'var(--yn-accent-soft)';

    const dragCls = dragOver === 'top'
        ? ' yn-vrow--drag-top'
        : dragOver === 'bottom'
            ? ' yn-vrow--drag-bottom'
            : '';

    return (
        <div
            className={`yn-vrow yn-tbl-line${active ? ' yn-vrow--active' : ''}${dragCls}${selected ? ' yn-vrow--selected' : ''}`}
            data-row-id={row.id}
            onClick={onClick}
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
                borderBottom: '1px solid var(--yn-line)',
                backgroundColor: rowBg,
                cursor: 'pointer',
                userSelect: 'none',
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
                <div className="yn-drag-handle" draggable onDragStart={onDragStart} title="Drag to reorder">
                    {DRAG_ICON}
                </div>
            </div>

            <div style={{ width: LEADING_CELL_WIDTHS.index, minWidth: LEADING_CELL_WIDTHS.index, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--yn-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                <span style={{ fontSize: 12 }}>{realIndex + 1}</span>
            </div>

            <div style={{ width: LEADING_CELL_WIDTHS.status, minWidth: LEADING_CELL_WIDTHS.status, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                {status === 'added' && <span className="yn-status-dot yn-status-dot--added" title="Added (not yet committed)" />}
                {status === 'changed' && <span className="yn-status-dot yn-status-dot--changed" title="Modified (not yet committed)" />}
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
    /** Total pixel width of all columns combined. */
    totalWidth: number;
    /** Data column header descriptors (right of the 4 fixed leading cells). */
    columnHeaders: TableColumnHeader[];
    /** Render the data cells for a live row. */
    renderDataCells: RenderDataCells<T>;
    /** Columns used in the removed-rows ghost section. */
    removedColumns: RemovedColumnDescriptor<T>[];
    /** Message shown when visibleRows is empty. */
    emptyMessage: string;
    /**
     * When true the body height is calculated with zero extra gap below the footer,
     * pinning the footer flush to the page bottom. Defaults to false (legacy +20px gap).
     */
    flushFooter?: boolean;
    /**
     * Optional actions rendered to the right of the column header row.
     * Pass <DraftActionButtons .../> here to preserve the save/discard/delete cluster.
     */
    headerActions?: React.ReactNode;
}

/**
 * Props a draft page supplies to a VirtualDraftTable wrapper.
 *
 * The page-supplied subset of VirtualDraftTableProps plus the draft-action
 * controls; the table-internal layout props (column descriptors, widths,
 * nouns, footer) are filled in by the concrete table component.
 */
export type VirtualDraftTableBaseProps<T extends { id: string }> = Pick<
    VirtualDraftTableProps<T>,
    | 'allRows'
    | 'visibleRows'
    | 'statusById'
    | 'removedRows'
    | 'activeRowId'
    | 'editingRowId'
    | 'selectedIds'
    | 'dragOverState'
    | 'onRowClick'
    | 'onEditRow'
    | 'onRestoreRow'
    | 'onSelectionChange'
    | 'onDragStart'
    | 'onDragOver'
    | 'onDragLeave'
    | 'onDrop'
> & {
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
};

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
    totalWidth,
    columnHeaders,
    renderDataCells,
    removedColumns,
    emptyMessage,
    flushFooter = false,
    headerActions,
}: VirtualDraftTableProps<T>): React.JSX.Element => {
    const scrollRef = useRef<HTMLDivElement | null>(null);
    const wrapRef = useRef<HTMLDivElement>(null);
    const bodyHeight = useContainerHeight(scrollRef as React.RefObject<HTMLElement | null>, 300, flushFooter ? FOOTER_HEIGHT : FOOTER_HEIGHT + 20);

    const setScrollRef = useCallback((el: HTMLDivElement | null): void => {
        (scrollRef as React.MutableRefObject<HTMLDivElement | null>).current = el;
    }, []);

    const rowVirtualizer = useVirtualizer({
        count: visibleRows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    const handleRowClick = useCallback((id: string): void => {
        onRowClick(id);
        onEditRow(id);
    }, [onRowClick, onEditRow]);

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
        <div ref={wrapRef} className="yn-table-wrap">
            <div className="yn-table-header-row">
                <div className="yn-vtbl-header yn-tbl-line" style={{ height: HEADER_HEIGHT, minWidth: totalWidth }}>
                    <div
                        style={{ width: LEADING_CELL_WIDTHS.checkbox, minWidth: LEADING_CELL_WIDTHS.checkbox, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
                    </div>
                    <div style={{ width: LEADING_CELL_WIDTHS.handle, minWidth: LEADING_CELL_WIDTHS.handle, flexShrink: 0 }} />
                    <div style={{ width: LEADING_CELL_WIDTHS.index, minWidth: LEADING_CELL_WIDTHS.index, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <span className="yn-th-text">#</span>
                    </div>
                    <div style={{ width: LEADING_CELL_WIDTHS.status, minWidth: LEADING_CELL_WIDTHS.status, flexShrink: 0 }} />
                    {columnHeaders.map((col) => (
                        <div key={col.label} style={{ width: col.width, minWidth: col.width, flexShrink: 0, display: 'flex', alignItems: 'center', paddingRight: 8 }}>
                            <span className="yn-th-text">{col.label}</span>
                        </div>
                    ))}
                </div>
                {headerActions}
            </div>

            <div
                ref={setScrollRef}
                className="yn-vtbl-body"
                style={bodyHeight > 0 ? { flex: '0 0 auto', height: bodyHeight } : undefined}
            >
                {visibleRows.length === 0 ? (
                    <div className="yn-table-empty">{emptyMessage}</div>
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
                                    onClick={() => handleRowClick(row.id)}
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

            <div className="yn-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="yn-toolbar__count">{footerText}</span>
            </div>
        </div>
    );
};
