import React, { useCallback, useMemo, useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { Checkbox, Tooltip } from '@gravity-ui/uikit';
import { Pencil, TrashBin } from '@gravity-ui/icons';
import { useContainerHeight } from '../../../hooks';
import { useRowHoverOverlay, RowHoverEditOverlay } from '../../_shared/draft';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { formatUnixSeconds } from '../../../utils';
import { ipAddressToString } from '../../../utils/netip';
import { getNeighbourId } from './utils';
import { nudStateToName, getStateMeta } from './stateMeta';
import { getMergeDebug } from './mergeDebug';
import type { SortableColumn, SortState } from './types';

const ROW_HEIGHT = 44;
const HEADER_HEIGHT = 40;
const FOOTER_HEIGHT = 28;
const OVERSCAN = 15;

const COL_CHECKBOX = 38;
const COL_INDEX = 48;
const COL_NEXT_HOP = 230;
const COL_LINK_ADDR = 150;
const COL_HW_ADDR = 150;
const COL_DEVICE = 90;
const COL_STATE = 140;
const COL_SOURCE = 160;
const COL_PRIORITY = 70;
const COL_UPDATED = 160;

const NEIGH_TOTAL_WIDTH =
    COL_CHECKBOX + COL_INDEX + COL_NEXT_HOP + COL_LINK_ADDR + COL_HW_ADDR +
    COL_DEVICE + COL_STATE + COL_SOURCE + COL_PRIORITY + COL_UPDATED;

export interface NeighbourTableProps {
    rows: Neighbour[];
    /** Pre-filter total row count — used in the footer summary. */
    totalCount: number;
    /** Whether a search filter is currently active. */
    searchActive: boolean;
    /** Optional right-side footer note (e.g. read-only warning). */
    readOnlyNote?: React.ReactNode;
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
    isMergedView?: boolean;
    cache?: Map<string, Neighbour[]>;
    tables?: NeighbourTableInfo[];
}

interface StateBadgeProps {
    state: number | undefined | null;
    withTooltip?: boolean;
}

/** Colored pill badge for a NUD state, with an optional explanatory tooltip. */
const StateBadge: React.FC<StateBadgeProps> = ({ state, withTooltip }) => {
    const name = nudStateToName(state);
    const meta = getStateMeta(state);

    const badge = (
        <span
            className="nb-state-badge"
            style={{
                '--nb-stb-c': meta.color,
                '--nb-stb-bg': `color-mix(in srgb, ${meta.color} 14%, transparent)`,
                '--nb-stb-bd': `color-mix(in srgb, ${meta.color} 32%, transparent)`,
            } as React.CSSProperties}
        >
            <span className="nb-state-badge__dot" />
            {name}
        </span>
    );

    if (!withTooltip) return badge;

    const tooltipContent = (
        <div className="nb-state-tip">
            <div className="nb-state-tip__head">
                <span className="nb-state-tip__dot" style={{ background: meta.color }} />
                {name}
            </div>
            <div className="nb-state-tip__desc">{meta.desc}</div>
            <div className="nb-state-tip__action">
                <strong>What to do:</strong> {meta.action}
            </div>
        </div>
    );

    return (
        <Tooltip content={tooltipContent} openDelay={200} placement="bottom" className="nb-state-tip-popup">
            {badge}
        </Tooltip>
    );
};

interface SourceCellProps {
    neighbour: Neighbour;
    isMergedView: boolean;
    cache?: Map<string, Neighbour[]>;
    tables?: NeighbourTableInfo[];
}

/** Source cell — in Merged view, shows override badge and MAC-conflict warning. */
const SourceCell: React.FC<SourceCellProps> = ({ neighbour, isMergedView, cache, tables }) => {
    const sourceName = neighbour.source || '-';
    const isStatic = sourceName === 'static';

    if (!isMergedView || !cache || !tables) {
        return <span className={`nb-src-name${isStatic ? ' nb-src-name--static' : ''}`}>{sourceName}</span>;
    }

    const { shadowed, macConflict } = getMergeDebug(neighbour, cache, tables);
    const hasOverride = shadowed.length > 0;
    const firstShadowedTable = shadowed[0]?.table ?? '';
    const shadowedTableNames = shadowed.map((s) => s.table).join(', ');
    const shadowedCount = shadowed.length;

    const overrideTooltip = hasOverride ? (
        <div className="nb-ovr-tip">
            <div className="nb-ovr-tip__head">
                <span>Merge override</span>
            </div>
            <div>
                Wins with the lowest priority <strong>{neighbour.priority ?? '?'}</strong> (higher precedence). Shadows{' '}
                {shadowedCount} lower-precedence {shadowedCount === 1 ? 'entry' : 'entries'} from{' '}
                <strong>{shadowedTableNames}</strong>.
            </div>
            {macConflict && (
                <div className="nb-ovr-tip__conflict">
                    <strong>MAC differs</strong> from the shadowed entry — click the row to compare.
                </div>
            )}
        </div>
    ) : null;

    return (
        <div className="nb-src-chip">
            <span className={`nb-src-name${isStatic ? ' nb-src-name--static' : ''}`}>{sourceName}</span>
            {hasOverride && overrideTooltip && (
                <Tooltip content={overrideTooltip} openDelay={150} placement="bottom" className="nb-ovr-tip-popup">
                    <span className={`nb-ovr-badge${macConflict ? ' nb-ovr-badge--conflict' : ''}`}>
                        {macConflict ? '⚠' : '⊕'} overrides {firstShadowedTable}
                    </span>
                </Tooltip>
            )}
        </div>
    );
};

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
            <span className="yn-th-text">{label}</span>
            <span style={{ fontSize: 10, opacity: isActive ? 1 : 0.35 }}>{arrow}</span>
        </button>
    );
};

/** Read-only virtualized table for the Neighbour list. */
export const NeighbourTable: React.FC<NeighbourTableProps> = ({
    rows,
    totalCount,
    searchActive,
    readOnlyNote,
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
    isMergedView,
    cache,
    tables,
}) => {
    const scrollRef = useRef<HTMLDivElement>(null);
    const headerRef = useRef<HTMLDivElement>(null);
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

    const handleBodyScroll = useCallback((): void => {
        const bodyEl = scrollRef.current;
        const hdrEl = headerRef.current;
        if (bodyEl && hdrEl) {
            hdrEl.scrollLeft = bodyEl.scrollLeft;
        }
    }, []);

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
    const totalWidth = NEIGH_TOTAL_WIDTH;

    const footerText = useMemo(() => {
        const mergedSuffix = isMergedView ? ' merged' : '';
        const filteredSuffix = searchActive && rows.length < totalCount ? ' (filtered)' : '';
        return `Shown ${rows.length.toLocaleString()} of ${totalCount.toLocaleString()}${mergedSuffix}${filteredSuffix}`;
    }, [rows.length, totalCount, isMergedView, searchActive]);

    const showTableActions = canEditTable || canDeleteTable;

    return (
        <div className="yn-table-wrap">
            <div className="yn-table-header-row">
                <div ref={headerRef} className="yn-vtbl-header nb-vtbl-header" style={{ height: HEADER_HEIGHT }}>
                    <div style={{ display: 'flex', alignItems: 'center', minWidth: totalWidth, height: '100%', paddingLeft: 4, paddingRight: 4 }}>
                        <div
                            style={{ width: COL_CHECKBOX, minWidth: COL_CHECKBOX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                            onClick={(e) => e.stopPropagation()}
                        >
                            {isMergedView ? (
                                <Tooltip content="Merged is read-only — open a table tab to select and delete." placement="bottom" openDelay={200}>
                                    <Checkbox checked={false} indeterminate={false} onUpdate={() => {}} size="m" disabled />
                                </Tooltip>
                            ) : (
                                <Checkbox checked={isAllSelected} indeterminate={isIndeterminate} onUpdate={handleSelectAll} size="m" />
                            )}
                        </div>
                        <div style={{ width: COL_INDEX, minWidth: COL_INDEX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                            <span className="yn-th-text">#</span>
                        </div>
                        <SortButton col="next_hop" label="Next Hop" width={COL_NEXT_HOP} sortState={sortState} onSort={onSort} />
                        <SortButton col="link_addr" label="Neighbour MAC" width={COL_LINK_ADDR} sortState={sortState} onSort={onSort} />
                        <SortButton col="hardware_addr" label="Interface MAC" width={COL_HW_ADDR} sortState={sortState} onSort={onSort} />
                        <SortButton col="device" label="Device" width={COL_DEVICE} sortState={sortState} onSort={onSort} />
                        <SortButton col="state" label="State" width={COL_STATE} sortState={sortState} onSort={onSort} />
                        <SortButton col="source" label="Source" width={COL_SOURCE} sortState={sortState} onSort={onSort} />
                        <Tooltip content="Lower value wins the merge (higher precedence)." placement="bottom" openDelay={200}>
                            <span style={{ display: 'contents' }}>
                                <SortButton col="priority" label="Priority" width={COL_PRIORITY} sortState={sortState} onSort={onSort} />
                            </span>
                        </Tooltip>
                        <SortButton col="updated_at" label="Updated At" width={COL_UPDATED} sortState={sortState} onSort={onSort} />
                    </div>
                </div>
                {showTableActions && (
                    <div className="yn-table-actions">
                        <button
                            type="button"
                            className="yn-row-edit-btn yn-row-edit-btn--visible"
                            title="Edit table"
                            aria-label="Edit table settings"
                            disabled={!canEditTable}
                            onClick={onEditTable}
                        >
                            <Pencil width={14} height={14} />
                        </button>
                        <button
                            type="button"
                            className="yn-row-edit-btn yn-row-edit-btn--visible yn-row-edit-btn--danger"
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
                className="yn-vtbl-body"
                style={bodyHeight > 0 ? { flex: '0 0 auto', height: bodyHeight } : undefined}
                onScroll={handleBodyScroll}
            >
                {rows.length === 0 ? (
                    <div className="yn-table-empty">{emptyMessage}</div>
                ) : (
                    <div style={{ height: rowVirtualizer.getTotalSize(), minWidth: totalWidth, position: 'relative' }}>
                        {virtualRows.map((virtualRow) => {
                            const neighbour = rows[virtualRow.index];
                            if (!neighbour) return null;
                            const id = getNeighbourId(neighbour);
                            const isSelected = selectedIds.has(id);
                            const isActive = activeRowId === id || editingRowId === id;
                            const rowBg = (isSelected || isActive) ? 'var(--yn-accent-soft)' : 'transparent';
                            return (
                                <div
                                    key={id || virtualRow.index}
                                    className={`yn-vrow${isActive ? ' yn-vrow--active' : ''}${isSelected ? ' yn-vrow--selected' : ''}`}
                                    data-row-id={id}
                                    onMouseEnter={() => handleHoverChange(neighbour, virtualRow.start)}
                                    onMouseLeave={() => handleHoverChange(null, 0)}
                                    onClick={() => onRowClick(id)}
                                    style={{
                                        position: 'absolute',
                                        top: virtualRow.start,
                                        left: 0,
                                        height: ROW_HEIGHT,
                                        minWidth: totalWidth,
                                        width: '100%',
                                        display: 'flex',
                                        alignItems: 'center',
                                        borderBottom: '1px solid var(--yn-line)',
                                        backgroundColor: rowBg,
                                        paddingLeft: 4,
                                        cursor: 'pointer',
                                    }}
                                >
                                    <div
                                        style={{ width: COL_CHECKBOX, minWidth: COL_CHECKBOX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center' }}
                                        onClick={(e) => e.stopPropagation()}
                                    >
                                        {isMergedView ? (
                                            <Tooltip content="Merged is read-only — open a table tab to select and delete." placement="bottom" openDelay={200}>
                                                <Checkbox checked={false} onUpdate={() => {}} size="m" disabled />
                                            </Tooltip>
                                        ) : (
                                            <Checkbox
                                                checked={isSelected}
                                                onUpdate={(checked) => handleRowCheckbox(id, checked)}
                                                size="m"
                                            />
                                        )}
                                    </div>
                                    <div style={{ width: COL_INDEX, minWidth: COL_INDEX, flexShrink: 0, display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--yn-text-3)', fontVariantNumeric: 'tabular-nums' }}>
                                        <span style={{ fontSize: 12 }}>{virtualRow.index + 1}</span>
                                    </div>
                                    <div style={{ width: COL_NEXT_HOP, minWidth: COL_NEXT_HOP, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="yn-cell-mono yn-cell-strong">{ipAddressToString(neighbour.next_hop) || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_LINK_ADDR, minWidth: COL_LINK_ADDR, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="yn-cell-mono yn-cell-muted">{neighbour.link_addr?.addr || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_HW_ADDR, minWidth: COL_HW_ADDR, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="yn-cell-mono yn-cell-muted">{neighbour.hardware_addr?.addr || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_DEVICE, minWidth: COL_DEVICE, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="yn-cell-muted">{neighbour.device || '-'}</span>
                                    </div>
                                    <div style={{ width: COL_STATE, minWidth: COL_STATE, flexShrink: 0, paddingRight: 8, overflow: 'hidden', whiteSpace: 'nowrap' }}>
                                        <StateBadge state={neighbour.state} withTooltip />
                                    </div>
                                    <div style={{ width: COL_SOURCE, minWidth: COL_SOURCE, flexShrink: 0, paddingRight: 8, overflow: 'hidden' }}>
                                        <SourceCell
                                            neighbour={neighbour}
                                            isMergedView={isMergedView ?? false}
                                            cache={cache}
                                            tables={tables}
                                        />
                                    </div>
                                    <div style={{ width: COL_PRIORITY, minWidth: COL_PRIORITY, flexShrink: 0, paddingRight: 8 }}>
                                        <span className="yn-cell-muted">{neighbour.priority ?? '-'}</span>
                                    </div>
                                    <div style={{ width: COL_UPDATED, minWidth: COL_UPDATED, flexShrink: 0, paddingRight: 8, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                                        <span className="yn-cell-muted">{formatUnixSeconds(neighbour.updated_at)}</span>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>

            <div className="yn-vtbl-footer" style={{ height: FOOTER_HEIGHT }}>
                <span className="yn-toolbar__count">{footerText}</span>
                {readOnlyNote}
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
                    editIcon={<Pencil width={14} height={14} />}
                    deleteIcon={<TrashBin width={14} height={14} />}
                />
            )}
        </div>
    );
};
