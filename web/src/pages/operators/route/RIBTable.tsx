import React, { useCallback, useEffect, useMemo, useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox } from '@gravity-ui/uikit';
import { useRowHoverOverlay, RowHoverEditOverlay } from '../../_shared/draft';
import type { Route } from '../../../api/routes';
import { ipAddressToString } from '../../../utils/netip';
import { useContainerHeight } from '../../../hooks/useContainerHeight';
import { getRouteId } from './utils';
import { BestPill, SourceChip, FamilyBadge, ConflictBadge } from './cells';
import type { RouteSortableColumn, RouteSortState } from './types';

const ROW_HEIGHT = 44;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

/** Minimum total width before a horizontal scrollbar appears on very narrow viewports. */
const RIB_MIN_WIDTH = 860;

/** CSS grid template for both the header row and each data row. */
const GRID_TEMPLATE = '38px 52px minmax(190px, 1.3fr) 150px minmax(120px, 0.8fr) 92px 64px 78px 116px';
const GRID_COLUMN_GAP = 14;

/** @deprecated Use GRID_TEMPLATE instead. Kept for external callers. */
export const RIB_TOTAL_WIDTH = RIB_MIN_WIDTH;

export interface RIBTableProps {
    rows: Route[];
    selectedIds: Set<string>;
    sortState: RouteSortState;
    onSort: (col: RouteSortableColumn) => void;
    onEditRow: (id: string) => void;
    onSelectionChange: (ids: Set<string>) => void;
    emptyMessage: string;
    onDeleteRow: (id: string) => void;
    conflictMap?: Map<string, number>;
    flashRowId?: string | null;
}

interface SortButtonProps {
    col: RouteSortableColumn;
    label: string;
    sortState: RouteSortState;
    onSort: (col: RouteSortableColumn) => void;
}

/** SVG sort icon — double arrow (unsorted), up arrow (asc), down arrow (desc). */
const SortIcon: React.FC<{ variant: 'sort' | 'sortUp' | 'sortDown'; active: boolean }> = ({ variant, active }) => {
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

const SortButton: React.FC<SortButtonProps> = ({ col, label, sortState, onSort }) => {
    const isActive = sortState.column === col;
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
                color: isActive ? 'var(--fw-accent)' : 'inherit',
                padding: 0,
                width: '100%',
                minWidth: 0,
            }}
            onClick={() => onSort(col)}
        >
            <span className="fw-th-text" style={{ color: isActive ? 'var(--fw-accent)' : undefined }}>{label}</span>
            <SortIcon variant={iconVariant} active={isActive} />
        </button>
    );
};

/** Read-only virtualized table for the RIB (Route Information Base). */
export const RIBTable: React.FC<RIBTableProps> = ({
    rows,
    selectedIds,
    sortState,
    onSort,
    onEditRow,
    onSelectionChange,
    emptyMessage,
    onDeleteRow,
    conflictMap,
    flashRowId,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);
    const bodyHeight = useContainerHeight(scrollRef, 300, FOOTER_HEIGHT);
    const [flashingId, setFlashingId] = React.useState<string | null>(null);

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

    useEffect(() => {
        if (!flashRowId) return;
        const idx = rows.findIndex((r) => getRouteId(r) === flashRowId);
        if (idx >= 0) {
            rowVirtualizer.scrollToIndex(idx, { align: 'center' });
        }
        setFlashingId(flashRowId);
        const t = setTimeout(() => setFlashingId(null), 1200);
        return () => clearTimeout(t);
    }, [flashRowId, rows, rowVirtualizer]);

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
            onEditRow(getRouteId(hoveredRow));
        }
    }, [hoveredRow, onEditRow]);

    const virtualRows = rowVirtualizer.getVirtualItems();

    const footerText = useMemo(() => {
        if (rows.length === 0 || virtualRows.length === 0) return '';
        const first = virtualRows[0].index + 1;
        const last = virtualRows[virtualRows.length - 1].index + 1;
        return `Shown ${first.toLocaleString()}–${last.toLocaleString()} of ${rows.length.toLocaleString()}`;
    }, [virtualRows, rows.length]);

    const gridStyle: React.CSSProperties = {
        display: 'grid',
        gridTemplateColumns: GRID_TEMPLATE,
        columnGap: GRID_COLUMN_GAP,
        alignItems: 'center',
        paddingLeft: 16,
        paddingRight: 22,
        minWidth: RIB_MIN_WIDTH,
    };

    return (
        <div className="fw-tbl-wrap">
            <div className="fw-tbl-header-row">
                <div
                    className="fw-vtbl-header"
                    style={{ ...gridStyle, height: HEADER_HEIGHT, flex: '1 1 auto', overflow: 'hidden' }}
                >
                    <div
                        style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                        onClick={(e) => e.stopPropagation()}
                    >
                        <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <span className="fw-th-text">#</span>
                    </div>
                    <SortButton col="prefix" label="Prefix" sortState={sortState} onSort={onSort} />
                    <SortButton col="next_hop" label="Next Hop" sortState={sortState} onSort={onSort} />
                    <SortButton col="peer" label="Peer" sortState={sortState} onSort={onSort} />
                    <SortButton col="is_best" label="Best" sortState={sortState} onSort={onSort} />
                    <SortButton col="pref" label="Pref" sortState={sortState} onSort={onSort} />
                    <SortButton col="as_path_len" label="AS Path" sortState={sortState} onSort={onSort} />
                    <SortButton col="source" label="Source" sortState={sortState} onSort={onSort} />
                </div>
            </div>

            <div
                ref={setScrollRef}
                className="fw-vtbl-body"
                style={{ flex: '0 0 auto', height: bodyHeight, overflowY: 'auto' }}
            >
                {rows.length === 0 ? (
                    <div className="fw-table-empty">{emptyMessage}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth: RIB_MIN_WIDTH, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const route = rows[virtualRow.index];
                            if (!route) return null;
                            const id = getRouteId(route);
                            const isSelected = selectedIds.has(id);
                            const isFlashing = flashingId === id;
                            let rowBg = 'transparent';
                            if (isFlashing) rowBg = 'color-mix(in srgb, var(--g-color-text-positive) 14%, transparent)';
                            else if (isSelected) rowBg = 'var(--fw-accent-soft)';
                            const prefixConflictCount = conflictMap?.get(route.prefix || '') ?? 1;
                            return (
                                <div
                                    key={id}
                                    className={`fw-vrow${isSelected ? ' fw-vrow--selected' : ''}`}
                                    data-row-id={id}
                                    onMouseEnter={() => handleHoverChange(route, virtualRow.start)}
                                    onMouseLeave={() => handleHoverChange(null, 0)}
                                    style={{
                                        position: 'absolute',
                                        top: virtualRow.start,
                                        left: 0,
                                        height: ROW_HEIGHT,
                                        width: '100%',
                                        borderBottom: '1px solid var(--fw-line)',
                                        backgroundColor: rowBg,
                                        cursor: 'default',
                                        ...gridStyle,
                                    }}
                                >
                                    <div
                                        style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                                        onClick={(e) => e.stopPropagation()}
                                    >
                                        <Checkbox
                                            checked={isSelected}
                                            onUpdate={(checked) => handleRowCheckbox(id, checked)}
                                            size="m"
                                        />
                                    </div>
                                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--fw-text-3)', fontVariantNumeric: 'tabular-nums', fontFamily: 'var(--fw-font-mono)' }}>
                                        <span style={{ fontSize: 12.5 }}>{virtualRow.index + 1}</span>
                                    </div>
                                    <div style={{ display: 'flex', alignItems: 'center', gap: 9, overflow: 'hidden' }}>
                                        {route.prefix && <FamilyBadge prefix={route.prefix} />}
                                        <span
                                            className="fw-cell-mono"
                                            style={{
                                                overflow: 'hidden',
                                                textOverflow: 'ellipsis',
                                                whiteSpace: 'nowrap',
                                                flexShrink: 1,
                                                fontSize: 13.5,
                                                fontWeight: route.is_best ? 600 : 500,
                                                color: route.is_best ? 'var(--fw-text)' : 'var(--fw-text-2)',
                                            }}
                                        >{route.prefix || '-'}</span>
                                        {prefixConflictCount > 1 && <ConflictBadge count={prefixConflictCount} />}
                                    </div>
                                    <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono" style={{ fontSize: 12.5, color: 'var(--fw-text-2)' }}>{ipAddressToString(route.next_hop) || '-'}</span>
                                    </div>
                                    <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="fw-cell-mono" style={{ fontSize: 12.5, color: 'var(--fw-text-3)' }}>{ipAddressToString(route.peer) || '-'}</span>
                                    </div>
                                    <div style={{ display: 'flex', alignItems: 'center' }}>
                                        <BestPill isBest={route.is_best ?? false} />
                                    </div>
                                    <div>
                                        <span className="fw-cell-mono" style={{ fontSize: 12.5, color: route.pref != null ? 'var(--fw-text-2)' : 'var(--fw-text-3)' }}>{route.pref ?? '—'}</span>
                                    </div>
                                    <div>
                                        <span className="fw-cell-mono" style={{ fontSize: 12.5, color: route.as_path_len != null ? 'var(--fw-text-2)' : 'var(--fw-text-3)' }}>{route.as_path_len ?? '—'}</span>
                                    </div>
                                    <div style={{ overflow: 'hidden' }}>
                                        <SourceChip source={route.source} />
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
