import React, { useCallback, useMemo, useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox } from '@gravity-ui/uikit';
import { Pencil, TrashBin } from '@gravity-ui/icons';
import { useContainerHeight } from '../../../hooks';
import { useRowHoverOverlay, RowHoverEditOverlay } from '../../_shared/draft';
import type { Neighbour } from '../../../api/neighbours';
import { formatUnixSeconds, getNUDStateString } from '../../../utils';
import { ipAddressToString } from '../../../utils/netip';
import { getNeighbourId } from './utils';
import type { SortableColumn, SortState } from './types';

const ROW_HEIGHT = 44;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

const COL_CHECKBOX = 38;
const COL_INDEX = 48;
const COL_NEXT_HOP = 260;
const COL_LINK_ADDR = 170;
const COL_HW_ADDR = 170;
const COL_DEVICE = 110;
const COL_STATE = 110;
const COL_SOURCE = 110;
const COL_PRIORITY = 80;
const COL_UPDATED = 180;

const NEIGH_TOTAL_WIDTH =
    COL_CHECKBOX + COL_INDEX + COL_NEXT_HOP + COL_LINK_ADDR + COL_HW_ADDR +
    COL_DEVICE + COL_STATE + COL_SOURCE + COL_PRIORITY + COL_UPDATED;

export interface NeighbourTableProps {
    rows: Neighbour[];
    selectedIds: Set<string>;
    activeRowId: string | null;
    editingRowId: string | null;
    sortState: SortState;
    onSort: (col: SortableColumn) => void;
    onRowClick: (id: string) => void;
    onEditRow: (id: string) => void;
    onSelectionChange: (ids: Set<string>) => void;
    emptyMessage: string;
    canEditTable: boolean;
    canDeleteTable: boolean;
    onEditTable: () => void;
    onDeleteTable: () => void;
    onDeleteRow?: (id: string) => void;
    canEditRow?: boolean;
}

interface SortButtonProps {
    col: SortableColumn;
    label: string;
    width: number;
    sortState: SortState;
    onSort: (col: SortableColumn) => void;
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

/** Read-only virtualized table for the Neighbour list. */
export const NeighbourTable: React.FC<NeighbourTableProps> = ({
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
    canEditTable,
    canDeleteTable,
    onEditTable,
    onDeleteTable,
    onDeleteRow,
    canEditRow,
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
    } = useRowHoverOverlay<Neighbour>(HEADER_HEIGHT);

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

    const isAllSelected = rows.length > 0 && rows.every((r) => selectedIds.has(getNeighbourId(r)));
    const isIndeterminate = !isAllSelected && rows.some((r) => selectedIds.has(getNeighbourId(r)));

    const handleSelectAll = useCallback((checked: boolean): void => {
        onSelectionChange(checked ? new Set(rows.map(getNeighbourId)) : new Set());
    }, [rows, onSelectionChange]);

    const handleRowCheckbox = useCallback((id: string, checked: boolean): void => {
        const next = new Set(selectedIds);
        if (checked) next.add(id); else next.delete(id);
        onSelectionChange(next);
    }, [selectedIds, onSelectionChange]);

    const handleOverlayEdit = useCallback((): void => {
        if (hoveredRow) {
            const id = getNeighbourId(hoveredRow);
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

    const showTableActions = canEditTable || canDeleteTable;

    return (
        <div className="fw-tbl-wrap">
            <div className="fw-tbl-header-row">
                <div className="fw-vtbl-header" style={{ height: HEADER_HEIGHT, minWidth: NEIGH_TOTAL_WIDTH }}>
                    <div
                        style={{ width: COL_CHECKBOX, minWidth: COL_CHECKBOX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
                    </div>
                    <div style={{ width: COL_INDEX, minWidth: COL_INDEX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <span className="fw-th-text">#</span>
                    </div>
                    <SortButton col="next_hop" label="Next Hop" width={COL_NEXT_HOP} sortState={sortState} onSort={onSort} />
                    <SortButton col="link_addr" label="Neighbour MAC" width={COL_LINK_ADDR} sortState={sortState} onSort={onSort} />
                    <SortButton col="hardware_addr" label="Interface MAC" width={COL_HW_ADDR} sortState={sortState} onSort={onSort} />
                    <SortButton col="device" label="Device" width={COL_DEVICE} sortState={sortState} onSort={onSort} />
                    <SortButton col="state" label="State" width={COL_STATE} sortState={sortState} onSort={onSort} />
                    <SortButton col="source" label="Source" width={COL_SOURCE} sortState={sortState} onSort={onSort} />
                    <SortButton col="priority" label="Priority" width={COL_PRIORITY} sortState={sortState} onSort={onSort} />
                    <SortButton col="updated_at" label="Updated At" width={COL_UPDATED} sortState={sortState} onSort={onSort} />
                </div>
                {showTableActions && (
                    <div className="fw-tbl-actions">
                        <button
                            type="button"
                            className="fw-tbl-action-btn"
                            title="Edit table"
                            aria-label="Edit table settings"
                            disabled={!canEditTable}
                            onClick={onEditTable}
                        >
                            <Pencil width={14} height={14} />
                        </button>
                        <button
                            type="button"
                            className="fw-tbl-action-btn fw-tbl-action-btn--delete"
                            title={canDeleteTable ? 'Delete table' : 'Cannot remove built-in table'}
                            aria-label="Delete table"
                            disabled={!canDeleteTable}
                            onClick={onDeleteTable}
                        >
                            <TrashBin width={14} height={14} />
                        </button>
                    </div>
                )}
            </div>

            <div
                ref={setScrollRef}
                className="fw-vtbl-body"
                style={bodyHeight > 0 ? { flex: '0 0 auto', height: bodyHeight } : undefined}
            >
                {rows.length === 0 ? (
                    <div className="fw-table-empty">{emptyMessage}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth: NEIGH_TOTAL_WIDTH, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const neighbour = rows[virtualRow.index];
                            if (!neighbour) return null;
                            const id = getNeighbourId(neighbour);
                            const isSelected = selectedIds.has(id);
                            const isActive = activeRowId === id || editingRowId === id;
                            const rowBg = (isSelected || isActive) ? 'var(--fw-accent-soft)' : 'transparent';
                            return (
                                <div
                                    key={id || virtualRow.index}
                                    className={`fw-vrow${isActive ? ' fw-vrow--active' : ''}${isSelected ? ' fw-vrow--selected' : ''}`}
                                    data-row-id={id}
                                    onMouseEnter={() => handleHoverChange(neighbour, virtualRow.start)}
                                    onMouseLeave={() => handleHoverChange(null, 0)}
                                    onClick={() => onRowClick(id)}
                                    style={{
                                        position: 'absolute',
                                        top: virtualRow.start,
                                        left: 0,
                                        height: ROW_HEIGHT,
                                        minWidth: NEIGH_TOTAL_WIDTH,
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
                                    <div style={{ width: COL_NEXT_HOP, minWidth: COL_NEXT_HOP, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono fw-cell-strong">{ipAddressToString(neighbour.next_hop) || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_LINK_ADDR, minWidth: COL_LINK_ADDR, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono fw-cell-muted">{neighbour.link_addr?.addr || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_HW_ADDR, minWidth: COL_HW_ADDR, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono fw-cell-muted">{neighbour.hardware_addr?.addr || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_DEVICE, minWidth: COL_DEVICE, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-muted">{neighbour.device || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_STATE, minWidth: COL_STATE, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-muted">{getNUDStateString(neighbour.state)}</span>
                                    </div>
                                    <div style={{ width: COL_SOURCE, minWidth: COL_SOURCE, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-muted">{neighbour.source || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_PRIORITY, minWidth: COL_PRIORITY, flexShrink: 0, paddingRight: 8 }}>
                                        <span className="fw-cell-muted">{neighbour.priority ?? '-'}</span>
                                    </div>
                                    <div style={{ width: COL_UPDATED, minWidth: COL_UPDATED, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-muted">{formatUnixSeconds(neighbour.updated_at)}</span>
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

            {canEditRow !== false && hoveredRow !== null && (
                <RowHoverEditOverlay
                    top={overlayTopOffset}
                    rowHeight={ROW_HEIGHT}
                    onEdit={handleOverlayEdit}
                    editAriaLabel={`Edit neighbour ${ipAddressToString(hoveredRow.next_hop) || (rows.indexOf(hoveredRow) + 1)}`}
                    editTitle="Edit neighbour"
                    onDelete={onDeleteRow ? () => onDeleteRow(getNeighbourId(hoveredRow)) : undefined}
                    deleteAriaLabel={`Delete neighbour ${ipAddressToString(hoveredRow.next_hop) || ''}`.trim()}
                    deleteTitle="Delete neighbour"
                    onMouseEnter={handleOverlayMouseEnter}
                    onMouseLeave={handleOverlayMouseLeave}
                />
            )}
        </div>
    );
};
