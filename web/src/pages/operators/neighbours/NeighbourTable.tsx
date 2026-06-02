import React from 'react';
import { Tooltip } from '@gravity-ui/uikit';
import { Pencil, TrashBin } from '@gravity-ui/icons';
import type { Neighbour, NeighbourTableInfo } from '../../../api/neighbours';
import { formatUnixSeconds } from '../../../utils';
import { ipAddressToString } from '../../../utils/netip';
import { getNeighbourId } from './utils';
import { nudStateToName, getStateMeta } from './stateMeta';
import { getMergeDebug } from './mergeDebug';
import type { SortableColumn, SortState } from './types';
import { VirtualTable } from '../../_shared/table/VirtualTable';
import type { Column, SortState as VTSortState } from '../../_shared/table/VirtualTable';
import { FamilyBadge } from '../../_shared/table/cells';

const NEIGH_MIN_WIDTH = 1256;

export interface NeighbourTableProps {
    rows: Neighbour[];
    /** Pre-filter total row count — used in the footer summary. */
    totalCount: number;
    /** Whether a search filter is currently active. */
    searchActive: boolean;
    /** Optional right-side footer note (e.g. read-only warning). */
    readOnlyNote?: React.ReactNode;
    selectedIds: Set<string>;
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

/** Read-only virtualized table for the Neighbour list. */
export const NeighbourTable: React.FC<NeighbourTableProps> = ({
    rows,
    totalCount,
    searchActive,
    readOnlyNote,
    selectedIds,
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
}): React.JSX.Element => {
    const columns: Column<Neighbour>[] = [
        {
            key: 'next_hop',
            header: 'Next Hop',
            gridTrack: '230px',
            sortKey: 'next_hop',
            renderCell: (neighbour) => {
                const addr = ipAddressToString(neighbour.next_hop) || '-';
                return (
                    <div style={{ display: 'flex', alignItems: 'center', gap: 5, overflow: 'hidden', width: '100%' }}>
                        {addr !== '-' && <FamilyBadge address={addr} />}
                        <span className="yn-cell-mono yn-cell-strong" style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{addr}</span>
                    </div>
                );
            },
        },
        {
            key: 'link_addr',
            header: 'Neighbour MAC',
            gridTrack: '150px',
            sortKey: 'link_addr',
            renderCell: (neighbour) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', width: '100%' }}>
                    <span className="yn-cell-mono yn-cell-muted">{neighbour.link_addr?.addr || '-'}</span>
                </div>
            ),
        },
        {
            key: 'hardware_addr',
            header: 'Interface MAC',
            gridTrack: '150px',
            sortKey: 'hardware_addr',
            renderCell: (neighbour) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', width: '100%' }}>
                    <span className="yn-cell-mono yn-cell-muted">{neighbour.hardware_addr?.addr || '-'}</span>
                </div>
            ),
        },
        {
            key: 'device',
            header: 'Device',
            gridTrack: '90px',
            sortKey: 'device',
            renderCell: (neighbour) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', width: '100%' }}>
                    <span className="yn-cell-muted">{neighbour.device || '-'}</span>
                </div>
            ),
        },
        {
            key: 'state',
            header: 'State',
            gridTrack: '140px',
            sortKey: 'state',
            renderCell: (neighbour) => (
                <div style={{ overflow: 'hidden', whiteSpace: 'nowrap' }}>
                    <StateBadge state={neighbour.state} withTooltip />
                </div>
            ),
        },
        {
            key: 'source',
            header: 'Source',
            gridTrack: '160px',
            sortKey: 'source',
            renderCell: (neighbour) => (
                <div style={{ overflow: 'hidden', width: '100%' }}>
                    <SourceCell
                        neighbour={neighbour}
                        isMergedView={isMergedView ?? false}
                        cache={cache}
                        tables={tables}
                    />
                </div>
            ),
        },
        {
            key: 'priority',
            header: 'Priority',
            gridTrack: '70px',
            sortKey: 'priority',
            renderCell: (neighbour) => (
                <span className="yn-cell-muted">{neighbour.priority ?? '-'}</span>
            ),
        },
        {
            key: 'updated_at',
            header: 'Updated At',
            gridTrack: '160px',
            sortKey: 'updated_at',
            renderCell: (neighbour) => (
                <div style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', width: '100%' }}>
                    <span className="yn-cell-muted">{formatUnixSeconds(neighbour.updated_at)}</span>
                </div>
            ),
        },
    ];

    const adaptedSortState: VTSortState<string> = {
        column: sortState.column,
        direction: sortState.direction,
    };

    const handleSort = (key: string): void => {
        onSort(key as SortableColumn);
    };

    const handleEditRow = (id: string): void => {
        onRowClick(id);
        onEditRow(id);
    };

    const mergedDisabledTooltip = 'Merged is read-only — open a table tab to select and delete.';

    const showTableActions = canEditTable || canDeleteTable;

    const footerText = (() => {
        const mergedSuffix = isMergedView ? ' merged' : '';
        const filteredSuffix = searchActive && rows.length < totalCount ? ' (filtered)' : '';
        return `Shown ${rows.length.toLocaleString()} of ${totalCount.toLocaleString()}${mergedSuffix}${filteredSuffix}`;
    })();

    const headerActions = showTableActions ? (
        <>
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
        </>
    ) : undefined;

    return (
        <VirtualTable<Neighbour>
            rows={rows}
            columns={columns}
            getRowId={getNeighbourId}
            emptyMessage={emptyMessage}
            selectedIds={selectedIds}
            onSelectionChange={onSelectionChange}
            selectionDisabled={isMergedView}
            selectionDisabledTooltip={mergedDisabledTooltip}
            sortState={adaptedSortState}
            onSort={handleSort}
            onEditRow={handleEditRow}
            onDeleteRow={onDeleteRow ? (id) => onDeleteRow(id) : undefined}
            canEditRow={canEditRow}
            editAriaLabel={(neighbour) => `Edit neighbour ${ipAddressToString(neighbour.next_hop) || ''}`}
            deleteAriaLabel={(neighbour) => `Delete neighbour ${ipAddressToString(neighbour.next_hop) || ''}`.trim()}
            editTitle="Edit neighbour"
            deleteTitle="Delete neighbour"
            editIcon={<Pencil width={14} height={14} />}
            deleteIcon={<TrashBin width={14} height={14} />}
            onRowClick={onRowClick}
            headerActions={headerActions}
            footerSummary={footerText}
            footerExtra={readOnlyNote}
            minWidth={NEIGH_MIN_WIDTH}
        />
    );
};
