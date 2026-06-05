import React from 'react';
import type { Route } from '../../../api/routes';
import { ipAddressToString } from '../../../utils/netip';
import { getRouteId } from './utils';
import { BestPill, SourceChip, FamilyBadge, ConflictBadge } from './cells';
import type { RouteSortableColumn, RouteSortState } from './types';
import { VirtualTable } from '../../../components/VirtualTable';
import type { Column, SortState } from '../../../components/VirtualTable';

/** Minimum total width before a horizontal scrollbar appears on very narrow viewports. */
const RIB_MIN_WIDTH = 860;


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
    activeRowId?: string | null;
}

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
    activeRowId,
}) => {
    const columns: Column<Route>[] = [
        {
            key: 'prefix',
            header: 'Prefix',
            gridTrack: 'minmax(190px, 1.3fr)',
            sortKey: 'prefix',
            renderCell: (route) => {
                const prefixConflictCount = conflictMap?.get(route.prefix || '') ?? 1;
                return (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 9, overflow: 'hidden' }}>
                        {route.prefix && <FamilyBadge prefix={route.prefix} />}
                        <span
                            className="yn-cell-mono"
                            style={{
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                whiteSpace: 'nowrap',
                                flexShrink: 1,
                                fontSize: 13.5,
                                fontWeight: route.is_best ? 600 : 500,
                                color: route.is_best ? 'var(--yn-text)' : 'var(--yn-text-2)',
                            }}
                        >{route.prefix || '-'}</span>
                        {prefixConflictCount > 1 && <ConflictBadge count={prefixConflictCount} />}
                    </div>
                );
            },
        },
        {
            key: 'next_hop',
            header: 'Next Hop',
            gridTrack: '150px',
            sortKey: 'next_hop',
            renderCell: (route) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    <span className="yn-cell-mono" style={{ fontSize: 12.5, color: 'var(--yn-text-2)' }}>{ipAddressToString(route.next_hop) || '-'}</span>
                </div>
            ),
        },
        {
            key: 'peer',
            header: 'Peer',
            gridTrack: 'minmax(120px, 0.8fr)',
            sortKey: 'peer',
            renderCell: (route) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    <span className="yn-cell-mono" style={{ fontSize: 12.5, color: 'var(--yn-text-3)' }}>{ipAddressToString(route.peer) || '-'}</span>
                </div>
            ),
        },
        {
            key: 'is_best',
            header: 'Best',
            gridTrack: '92px',
            sortKey: 'is_best',
            renderCell: (route) => (
                <div style={{ display: 'flex', alignItems: 'center' }}>
                    <BestPill isBest={route.is_best ?? false} />
                </div>
            ),
        },
        {
            key: 'pref',
            header: 'Pref',
            gridTrack: '64px',
            sortKey: 'pref',
            renderCell: (route) => (
                <span className="yn-cell-mono" style={{ fontSize: 12.5, color: route.pref != null ? 'var(--yn-text-2)' : 'var(--yn-text-3)' }}>{route.pref ?? '—'}</span>
            ),
        },
        {
            key: 'as_path_len',
            header: 'AS Path',
            gridTrack: '78px',
            sortKey: 'as_path_len',
            renderCell: (route) => (
                <span className="yn-cell-mono" style={{ fontSize: 12.5, color: route.as_path_len != null ? 'var(--yn-text-2)' : 'var(--yn-text-3)' }}>{route.as_path_len ?? '—'}</span>
            ),
        },
        {
            key: 'source',
            header: 'Source',
            gridTrack: '116px',
            sortKey: 'source',
            renderCell: (route) => (
                <div style={{ overflow: 'hidden' }}>
                    <SourceChip source={route.source} />
                </div>
            ),
        },
    ];

    const adaptedSortState: SortState<string> = {
        column: sortState.column,
        direction: sortState.direction,
    };

    const handleSort = (key: string): void => {
        onSort(key as RouteSortableColumn);
    };

    return (
        <VirtualTable<Route>
            rows={rows}
            columns={columns}
            getRowId={getRouteId}
            emptyMessage={emptyMessage}
            selectedIds={selectedIds}
            onSelectionChange={onSelectionChange}
            sortState={adaptedSortState}
            onSort={handleSort}
            onEditRow={onEditRow}
            onDeleteRow={onDeleteRow}
            canEditRow={true}
            editAriaLabel={(_route, idx) => `Edit route ${idx + 1}`}
            deleteAriaLabel={(route) => `Delete route ${route.prefix || ''}`.trim()}
            editTitle="Edit route"
            deleteTitle="Delete route"
            flashRowId={flashRowId}
            activeRowId={activeRowId}
            scrollActiveIntoView
            minWidth={RIB_MIN_WIDTH}
        />
    );
};
