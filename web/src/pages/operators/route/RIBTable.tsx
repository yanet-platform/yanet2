import React, { useCallback, useMemo, useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox } from '@gravity-ui/uikit';
import { useContainerHeight } from '../../../hooks';
import { useRowHoverOverlay, RowHoverEditOverlay } from '../../_shared/draft';
import type { Route } from '../../../api/routes';
import { ipAddressToString } from '../../../utils/netip';
import { ROUTE_SOURCES, getRouteId } from './utils';
import type { RouteSortableColumn, RouteSortState } from './types';

const ROW_HEIGHT = 44;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

const COL_CHECKBOX = 38;
const COL_INDEX = 48;
const COL_PREFIX = 260;
const COL_NEXT_HOP = 200;
const COL_PEER = 200;
const COL_BEST = 60;
const COL_PREF = 60;
const COL_AS_PATH = 80;
const COL_SOURCE = 90;

export const RIB_TOTAL_WIDTH =
    COL_CHECKBOX + COL_INDEX + COL_PREFIX + COL_NEXT_HOP + COL_PEER +
    COL_BEST + COL_PREF + COL_AS_PATH + COL_SOURCE;

export interface RIBTableProps {
    rows: Route[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    editingRowId: string | null;
    sortState: RouteSortState;
    onSort: (col: RouteSortableColumn) => void;
    onRowClick: (id: string) => void;
    onEditRow: (id: string) => void;
    onSelectionChange: (ids: Set<string>) => void;
    emptyMessage: string;
    onDeleteRow: (id: string) => void;
}

interface SortButtonProps {
    col: RouteSortableColumn;
    label: string;
    width: number;
    sortState: RouteSortState;
    onSort: (col: RouteSortableColumn) => void;
}

const SortButton: React.FC<SortButtonProps> = ({ col, label, width, sortState, onSort }) => {
    const isActive = sortState.column === col;
    const arrow = isActive ? (sortState.direction === 'asc' ? '▲' : '▼') : '↕';
    return (
        <button
            type="button"
            style={{
                width,
                minWidth: width,
                flexShrink: 0,
                display: 'flex',
                alignItems: 'center',
                gap: 4,
                background: 'none',
                border: 'none',
                cursor: 'pointer',
                color: 'inherit',
                padding: '0 8px 0 0',
            }}
            onClick={() => onSort(col)}
        >
            <span className="fw-th-text">{label}</span>
            <span style={{ fontSize: 10, opacity: isActive ? 1 : 0.35 }}>{arrow}</span>
        </button>
    );
};

/** Read-only virtualized table for the RIB (Route Information Base). */
export const RIBTable: React.FC<RIBTableProps> = ({
    rows,
    selectedIds,
    activeRowId,
    editingRowId,
    sortState,
    onSort,
    onRowClick,
    onEditRow,
    onSelectionChange,
    emptyMessage,
    onDeleteRow,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);
    const bodyHeight = useContainerHeight(scrollRef, 300, FOOTER_HEIGHT + 20);

    const {
        hoveredRow,
        overlayTopOffset,
        handleHoverChange,
        handleOverlayMouseEnter,
        handleOverlayMouseLeave,
        attachScrollEl,
    } = useRowHoverOverlay<Route>(HEADER_HEIGHT);

    const setScrollRef = useCallback((el: HTMLDivElement | null): void => {
        (scrollRef as React.MutableRefObject<HTMLDivElement | null>).current = el;
        attachScrollEl(el);
    }, [attachScrollEl]);

    const rowVirtualizer = useVirtualizer({
        count: rows.length,
        getScrollElement: () => scrollRef.current,
        estimateSize: () => ROW_HEIGHT,
        overscan: OVERSCAN,
    });

    const isAllSelected = rows.length > 0 && rows.every((r) => selectedIds.has(getRouteId(r)));
    const isIndeterminate = !isAllSelected && rows.some((r) => selectedIds.has(getRouteId(r)));

    const handleSelectAll = useCallback((checked: boolean): void => {
        onSelectionChange(checked ? new Set(rows.map(getRouteId)) : new Set());
    }, [rows, onSelectionChange]);

    const handleRowCheckbox = useCallback((id: string, checked: boolean): void => {
        const next = new Set(selectedIds);
        if (checked) next.add(id); else next.delete(id);
        onSelectionChange(next);
    }, [selectedIds, onSelectionChange]);

    const handleOverlayEdit = useCallback((): void => {
        if (hoveredRow) {
            const id = getRouteId(hoveredRow);
            onRowClick(id);
            onEditRow(id);
        }
    }, [hoveredRow, onRowClick, onEditRow]);

    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = useMemo(() => {
        if (rows.length === 0 || virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${rows.length.toLocaleString()}`;
    }, [virtualRows, rows.length]);

    return (
        <div className="fw-tbl-wrap">
            <div className="fw-tbl-header-row">
                <div className="fw-vtbl-header" style={{ height: HEADER_HEIGHT, minWidth: RIB_TOTAL_WIDTH }}>
                    <div
                        style={{ width: COL_CHECKBOX, minWidth: COL_CHECKBOX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
                    </div>
                    <div style={{ width: COL_INDEX, minWidth: COL_INDEX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <span className="fw-th-text">#</span>
                    </div>
                    <SortButton col="prefix" label="Prefix" width={COL_PREFIX} sortState={sortState} onSort={onSort} />
                    <SortButton col="next_hop" label="Next Hop" width={COL_NEXT_HOP} sortState={sortState} onSort={onSort} />
                    <SortButton col="peer" label="Peer" width={COL_PEER} sortState={sortState} onSort={onSort} />
                    <SortButton col="is_best" label="Best" width={COL_BEST} sortState={sortState} onSort={onSort} />
                    <SortButton col="pref" label="Pref" width={COL_PREF} sortState={sortState} onSort={onSort} />
                    <SortButton col="as_path_len" label="AS Path" width={COL_AS_PATH} sortState={sortState} onSort={onSort} />
                    <SortButton col="source" label="Source" width={COL_SOURCE} sortState={sortState} onSort={onSort} />
                </div>
            </div>

            <div
                ref={setScrollRef}
                className="fw-vtbl-body"
                style={bodyHeight > 0 ? { flex: '0 0 auto', height: bodyHeight } : undefined}
            >
                {rows.length === 0 ? (
                    <div className="fw-table-empty">{emptyMessage}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth: RIB_TOTAL_WIDTH, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const route = rows[virtualRow.index];
                            if (!route) return null;
                            const id = getRouteId(route);
                            const isSelected = selectedIds.has(id);
                            const isActive = activeRowId === id || editingRowId === id;
                            let rowBg = 'transparent';
                            if (isSelected || isActive) rowBg = 'var(--fw-accent-soft)';
                            return (
                                <div
                                    key={id}
                                    className={`fw-vrow${isActive ? ' fw-vrow--active' : ''}${isSelected ? ' fw-vrow--selected' : ''}`}
                                    data-row-id={id}
                                    onMouseEnter={() => handleHoverChange(route, virtualRow.start)}
                                    onMouseLeave={() => handleHoverChange(null, 0)}
                                    onClick={() => onRowClick(id)}
                                    style={{
                                        position: 'absolute',
                                        top: virtualRow.start,
                                        left: 0,
                                        height: ROW_HEIGHT,
                                        minWidth: RIB_TOTAL_WIDTH,
                                        width: '100%',
                                        display: 'flex',
                                        alignItems: 'center',
                                        borderBottom: '1px solid var(--fw-line)',
                                        backgroundColor: rowBg,
                                        paddingLeft: 4,
                                        cursor: 'default',
                                    }}
                                >
                                    <div
                                        style={{ width: COL_CHECKBOX, minWidth: COL_CHECKBOX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                                        onClick={(e) => e.stopPropagation()}
                                    >
                                        <Checkbox
                                            checked={isSelected}
                                            onUpdate={(checked) => handleRowCheckbox(id, checked)}
                                            size="m"
                                        />
                                    </div>
                                    <div style={{ width: COL_INDEX, minWidth: COL_INDEX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--fw-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                                        <span style={{ fontSize: 12 }}>{virtualRow.index + 1}</span>
                                    </div>
                                    <div style={{ width: COL_PREFIX, minWidth: COL_PREFIX, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono fw-cell-strong">{route.prefix || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_NEXT_HOP, minWidth: COL_NEXT_HOP, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono fw-cell-muted">{ipAddressToString(route.next_hop) || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_PEER, minWidth: COL_PEER, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono fw-cell-muted">{ipAddressToString(route.peer) || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_BEST, minWidth: COL_BEST, flexShrink: 0, paddingRight: 8 }}>
                                        <span className="fw-cell-muted">{route.is_best ? 'Yes' : 'No'}</span>
                                    </div>
                                    <div style={{ width: COL_PREF, minWidth: COL_PREF, flexShrink: 0, paddingRight: 8 }}>
                                        <span className="fw-cell-muted">{route.pref ?? '-'}</span>
                                    </div>
                                    <div style={{ width: COL_AS_PATH, minWidth: COL_AS_PATH, flexShrink: 0, paddingRight: 8 }}>
                                        <span className="fw-cell-muted">{route.as_path_len ?? '-'}</span>
                                    </div>
                                    <div style={{ width: COL_SOURCE, minWidth: COL_SOURCE, flexShrink: 0, paddingRight: 8 }}>
                                        <span className="fw-cell-muted">
                                            {route.source !== undefined ? (ROUTE_SOURCES[route.source] ?? 'Unknown') : '-'}
                                        </span>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>

            <div className="fw-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="fw-toolbar__count">{footerText}</span>
            </div>

            {hoveredRow !== null && (
                <RowHoverEditOverlay
                    top={overlayTopOffset}
                    rowHeight={ROW_HEIGHT}
                    onEdit={handleOverlayEdit}
                    editAriaLabel={`Edit route ${rows.indexOf(hoveredRow) + 1}`}
                    editTitle="Edit route"
                    onDelete={() => onDeleteRow(getRouteId(hoveredRow))}
                    deleteAriaLabel={`Delete route ${hoveredRow.prefix || ''}`.trim()}
                    deleteTitle="Delete route"
                    onMouseEnter={handleOverlayMouseEnter}
                    onMouseLeave={handleOverlayMouseLeave}
                />
            )}
        </div>
    );
};
