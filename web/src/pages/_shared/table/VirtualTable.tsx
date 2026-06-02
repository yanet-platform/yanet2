import React, { useCallback, useEffect, useMemo, useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox, Tooltip } from '@gravity-ui/uikit';
import { useRowHoverOverlay, RowHoverEditOverlay } from '../draft';
import { useContainerHeight } from '../../../hooks/useContainerHeight';

export const ROW_HEIGHT = 44;
export const HEADER_HEIGHT = 40;
export const FOOTER_HEIGHT = 28;
export const OVERSCAN = 15;

const CHECKBOX_TRACK = '38px';
const INDEX_TRACK = '52px';

/** Column definition for VirtualTable. */
export interface Column<T> {
    /** Unique key, used as React key. */
    key: string;
    /** Header label text. */
    header: string;
    /** CSS grid track size, e.g. 'minmax(190px,1.3fr)' or '150px'. */
    gridTrack: string;
    /** When provided, the header becomes a sort button for this key. */
    sortKey?: string;
    /** Optional text alignment for the cell. */
    align?: 'left' | 'center' | 'right';
    /** Render the cell for a given row. */
    renderCell: (row: T, index: number) => React.ReactNode;
}

export interface SortState<K extends string> {
    column: K | null;
    direction: 'asc' | 'desc';
}

export interface VirtualTableProps<T> {
    rows: T[];
    columns: Column<T>[];
    getRowId: (row: T) => string;
    emptyMessage?: string;

    selectedIds: Set<string>;
    onSelectionChange: (ids: Set<string>) => void;
    selectionDisabled?: boolean;
    selectionDisabledTooltip?: React.ReactNode;

    sortState: SortState<string>;
    onSort: (key: string) => void;

    onEditRow: (id: string) => void;
    onDeleteRow?: (id: string) => void;
    canEditRow?: boolean;
    editAriaLabel?: (row: T, idx: number) => string;
    deleteAriaLabel?: (row: T, idx: number) => string;
    editTitle?: string;
    deleteTitle?: string;
    editIcon?: React.ReactNode;
    deleteIcon?: React.ReactNode;

    onRowClick?: (id: string) => void;
    activeRowId?: string | null;

    flashRowId?: string | null;

    headerActions?: React.ReactNode;

    footerSummary?: string;
    footerExtra?: React.ReactNode;

    minWidth: number;
}

/** SVG sort icon — double arrow (unsorted), up arrow (asc), down arrow (desc). */
export const SortIcon: React.FC<{ variant: 'sort' | 'sortUp' | 'sortDown'; active: boolean }> = ({ variant, active }) => {
    const paths: Record<typeof variant, string[]> = {
        sort: ['M8 3v18', 'M5 8l3-3 3 3', 'M16 21V3', 'M13 16l3 3 3-3'],
        sortUp: ['M8 5l4-4 4 4', 'M12 1v22'],
        sortDown: ['M8 19l4 4 4-4', 'M12 23V1'],
    };
    return (
        <svg
            width={12}
            height={12}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={2}
            strokeLinecap="round"
            strokeLinejoin="round"
            style={{ display: 'block', flexShrink: 0, opacity: active ? 1 : 0.5 }}
        >
            {paths[variant].map((d) => <path key={d} d={d} />)}
        </svg>
    );
};

interface SortButtonProps {
    sortKey: string;
    label: string;
    sortState: SortState<string>;
    onSort: (key: string) => void;
}

const SortButton: React.FC<SortButtonProps> = ({ sortKey, label, sortState, onSort }) => {
    const isActive = sortState.column === sortKey;
    const iconVariant = isActive
        ? (sortState.direction === 'asc' ? 'sortUp' : 'sortDown')
        : 'sort';
    return (
        <button
            type="button"
            style={{
                display: 'flex',
                alignItems: 'center',
                gap: 4,
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                color: isActive ? 'var(--yn-accent)' : 'inherit',
                padding: 0,
                width: '100%',
                minWidth: 0,
            }}
            onClick={() => onSort(sortKey)}
        >
            <span className="yn-th-text" style={{ color: isActive ? 'var(--yn-accent)' : undefined }}>{label}</span>
            <SortIcon variant={iconVariant} active={isActive} />
        </button>
    );
};

/**
 * Generic virtualized read-only table with CSS-grid columns, shared hover-edit overlay,
 * selection, sort, flash, active-row, and optional per-row click.
 */
export function VirtualTable<T>({
    rows,
    columns,
    getRowId,
    emptyMessage = 'No data.',
    selectedIds,
    onSelectionChange,
    selectionDisabled,
    selectionDisabledTooltip,
    sortState,
    onSort,
    onEditRow,
    onDeleteRow,
    canEditRow,
    editAriaLabel,
    deleteAriaLabel,
    editTitle = 'Edit',
    deleteTitle = 'Delete',
    editIcon,
    deleteIcon,
    onRowClick,
    activeRowId,
    flashRowId,
    headerActions,
    footerSummary,
    footerExtra,
    minWidth,
}: VirtualTableProps<T>): React.JSX.Element {
    const scrollRef = useRef<HTMLDivElement | null>(null);
    const headerRef = useRef<HTMLDivElement | null>(null);
    const bodyHeight = useContainerHeight(scrollRef as React.RefObject<HTMLElement | null>, 300, FOOTER_HEIGHT);
    const [flashingId, setFlashingId] = React.useState<string | null>(null);

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

    const handleBodyScroll = useCallback((e: React.UIEvent<HTMLDivElement>): void => {
        const body = e.currentTarget;
        const header = headerRef.current;
        if (header !== null) {
            header.scrollLeft = body.scrollLeft;
        }
    }, []);

    const rowVirtualizer = useVirtualizer({
        count: rows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    useEffect(() => {
        if (!flashRowId) return;
        const idx = rows.findIndex((r) => getRowId(r) === flashRowId);
        if (idx >= 0) {
            rowVirtualizer.scrollToIndex(idx, { align: 'center' });
        }
        setFlashingId(flashRowId);
        const t = setTimeout(() => setFlashingId(null), 1200);
        return () => clearTimeout(t);
    }, [flashRowId, rows, rowVirtualizer, getRowId]);

    const isAllSelected = rows.length > 0 && rows.every((r) => selectedIds.has(getRowId(r)));
    const isIndeterminate = !isAllSelected && rows.some((r) => selectedIds.has(getRowId(r)));

    const handleSelectAll = useCallback((checked: boolean): void => {
        onSelectionChange(checked ? new Set(rows.map(getRowId)) : new Set());
    }, [rows, onSelectionChange, getRowId]);

    const handleRowCheckbox = useCallback((id: string, checked: boolean): void => {
        const next = new Set(selectedIds);
        if (checked) next.add(id); else next.delete(id);
        onSelectionChange(next);
    }, [selectedIds, onSelectionChange]);

    const handleOverlayEdit = useCallback((): void => {
        if (hoveredRow) {
            onEditRow(getRowId(hoveredRow));
        }
    }, [hoveredRow, onEditRow, getRowId]);

    const virtualRows = rowVirtualizer.getVirtualItems();

    const defaultFooterText = useMemo(() => {
        if (rows.length === 0 || virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${rows.length.toLocaleString()}`;
    }, [virtualRows, rows.length]);

    const footerText = footerSummary ?? defaultFooterText;

    const gridTemplateColumns = [CHECKBOX_TRACK, INDEX_TRACK, ...columns.map((c) => c.gridTrack)].join(' ');
    const GRID_COLUMN_GAP = 14;

    const gridStyle: React.CSSProperties = {
        display: 'grid',
        gridTemplateColumns,
        columnGap: GRID_COLUMN_GAP,
        alignItems: 'center',
        paddingLeft: 16,
        paddingRight: 22,
        minWidth,
    };

    const headerCheckbox = selectionDisabled ? (
        <Tooltip content={selectionDisabledTooltip ?? 'Selection disabled'} placement="bottom" openDelay={200}>
            <Checkbox checked={false} indeterminate={false} onUpdate={() => {}} size="m" disabled />
        </Tooltip>
    ) : (
        <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
    );

    return (
        <div className="yn-table-wrap">
            <div className="yn-table-header-row">
                <div
                    ref={headerRef}
                    className="yn-vtbl-header yn-vtbl-header--grid"
                    style={{ flex: '1 1 auto', height: HEADER_HEIGHT, overflowX: 'scroll', overflowY: 'hidden', minWidth: 0 }}
                >
                    <div style={{ ...gridStyle, height: HEADER_HEIGHT, display: 'grid' }}>
                        <div
                            style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                            onClick={(e) => e.stopPropagation()}
                        >
                            {headerCheckbox}
                        </div>
                        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                            <span className="yn-th-text">#</span>
                        </div>
                        {columns.map((col) => (
                            <div
                                key={col.key}
                                style={{
                                    display: 'flex',
                                    alignItems: 'center',
                                    justifyContent: col.align === 'center' ? 'center' : col.align === 'right' ? 'flex-end' : 'flex-start',
                                    overflow: 'hidden',
                                }}
                            >
                                {col.sortKey ? (
                                    <SortButton
                                        sortKey={col.sortKey}
                                        label={col.header}
                                        sortState={sortState}
                                        onSort={onSort}
                                    />
                                ) : (
                                    <span className="yn-th-text">{col.header}</span>
                                )}
                            </div>
                        ))}
                    </div>
                </div>
                {headerActions && (
                    <div className="yn-table-actions">
                        {headerActions}
                    </div>
                )}
            </div>

            <div
                ref={setScrollRef}
                className="yn-vtbl-body"
                style={{ flex: '0 0 auto', height: bodyHeight, overflowY: 'auto' }}
                onScroll={handleBodyScroll}
            >
                {rows.length === 0 ? (
                    <div className="yn-table-empty">{emptyMessage}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const row = rows[virtualRow.index];
                            if (!row) return null;
                            const id = getRowId(row);
                            const isSelected = selectedIds.has(id);
                            const isActive = activeRowId === id;
                            const isFlashing = flashingId === id;
                            let rowBg = 'transparent';
                            if (isFlashing) rowBg = 'color-mix(in srgb, var(--g-color-text-positive) 14%, transparent)';
                            else if (isSelected || isActive) rowBg = 'var(--yn-accent-soft)';

                            const rowClasses = [
                                'yn-vrow',
                                isActive ? 'yn-vrow--active' : '',
                                isSelected ? 'yn-vrow--selected' : '',
                            ].filter(Boolean).join(' ');

                            const rowCheckbox = selectionDisabled ? (
                                <Tooltip
                                    content={selectionDisabledTooltip ?? 'Selection disabled'}
                                    placement="bottom"
                                    openDelay={200}
                                >
                                    <Checkbox checked={false} onUpdate={() => {}} size="m" disabled />
                                </Tooltip>
                            ) : (
                                <Checkbox
                                    checked={isSelected}
                                    onUpdate={(checked) => handleRowCheckbox(id, checked)}
                                    size="m"
                                />
                            );

                            return (
                                <div
                                    key={id || virtualRow.index}
                                    className={rowClasses}
                                    data-row-id={id}
                                    onMouseEnter={() => handleHoverChange(row, virtualRow.start)}
                                    onMouseLeave={() => handleHoverChange(null, 0)}
                                    onClick={onRowClick ? () => onRowClick(id) : undefined}
                                    style={{
                                        position: 'absolute',
                                        top: virtualRow.start,
                                        left: 0,
                                        height: ROW_HEIGHT,
                                        width: '100%',
                                        borderBottom: '1px solid var(--yn-line)',
                                        backgroundColor: rowBg,
                                        cursor: onRowClick ? 'pointer' : 'default',
                                        ...gridStyle,
                                    }}
                                >
                                    <div
                                        style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                                        onClick={(e) => e.stopPropagation()}
                                    >
                                        {rowCheckbox}
                                    </div>
                                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--yn-text-3)', fontVariantNumeric: 'tabular-nums', fontFamily: 'var(--yn-font-mono)' }}>
                                        <span style={{ fontSize: 12.5 }}>{virtualRow.index + 1}</span>
                                    </div>
                                    {columns.map((col) => (
                                        <div
                                            key={col.key}
                                            style={{
                                                overflow: 'hidden',
                                                display: 'flex',
                                                alignItems: 'center',
                                                justifyContent: col.align === 'center' ? 'center' : col.align === 'right' ? 'flex-end' : 'flex-start',
                                            }}
                                        >
                                            {col.renderCell(row, virtualRow.index)}
                                        </div>
                                    ))}
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>

            <div className="yn-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="yn-toolbar__count">{footerText}</span>
                {footerExtra}
            </div>

            {canEditRow !== false && hoveredRow !== null && (
                <RowHoverEditOverlay
                    top={overlayTopOffset}
                    rowHeight={ROW_HEIGHT}
                    onEdit={handleOverlayEdit}
                    editAriaLabel={editAriaLabel ? editAriaLabel(hoveredRow, rows.indexOf(hoveredRow)) : editTitle}
                    editTitle={editTitle}
                    onDelete={onDeleteRow ? () => onDeleteRow(getRowId(hoveredRow)) : undefined}
                    deleteAriaLabel={deleteAriaLabel ? deleteAriaLabel(hoveredRow, rows.indexOf(hoveredRow)) : deleteTitle}
                    deleteTitle={deleteTitle}
                    onMouseEnter={handleOverlayMouseEnter}
                    onMouseLeave={handleOverlayMouseLeave}
                    editIcon={editIcon}
                    deleteIcon={deleteIcon}
                />
            )}
        </div>
    );
}
