import React, { useMemo } from 'react';
import type { PrefixRowItem, PrefixRowStatus } from './types';
import { validateRow } from './validation';
import { VirtualDraftTable, LEADING_TOTAL_WIDTH } from '../../_shared/draft';
import type { RemovedColumnDescriptor, TableColumnHeader } from '../../_shared/draft';

const PREFIX_WIDTH = 480;

const TOTAL_WIDTH = LEADING_TOTAL_WIDTH + PREFIX_WIDTH;

const COLUMN_HEADERS: TableColumnHeader[] = [
    { width: PREFIX_WIDTH, label: 'Prefix' },
];

const REMOVED_COLUMNS: RemovedColumnDescriptor<PrefixRowItem>[] = [
    { width: PREFIX_WIDTH, render: (r) => <span className="fw-cell-mono">{r.prefix}</span> },
];

const renderPrefixDataCells = (row: PrefixRowItem): React.ReactNode => {
    const errors = validateRow(row);
    return (
        <div
            style={{
                width: PREFIX_WIDTH,
                minWidth: PREFIX_WIDTH,
                maxWidth: PREFIX_WIDTH,
                flexShrink: 0,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
                paddingRight: 8,
                display: 'flex',
                alignItems: 'center',
                ...(errors.prefix ? { color: 'var(--fw-danger)' } : {}),
            }}
            title={row.prefix || undefined}
        >
            <span className="fw-cell-mono fw-cell-strong">
                {row.prefix || <span style={{ color: 'var(--fw-text-3)', fontStyle: 'italic' }}>prefix?</span>}
            </span>
        </div>
    );
};

export interface PrefixTableProps {
    allRows: PrefixRowItem[];
    visibleRows: PrefixRowItem[];
    statusById: Map<string, PrefixRowStatus>;
    removedRows: PrefixRowItem[];
    activeRowId: string | null;
    editingRowId: string | null;
    selectedIds: Set<string>;
    dragOverState: { id: string | null; where: 'top' | 'bottom' | null };
    onRowClick: (id: string) => void;
    onEditRow: (id: string) => void;
    onRestoreRow: (row: PrefixRowItem) => void;
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

/** Virtualized prefix table backed by VirtualDraftTable. */
export const PrefixTable: React.FC<PrefixTableProps> = (props) => {
    const statusById = useMemo(
        () => props.statusById as Map<string, import('../../_shared/draft').RowStatus>,
        [props.statusById],
    );

    return (
        <VirtualDraftTable
            {...props}
            statusById={statusById}
            totalWidth={TOTAL_WIDTH}
            columnHeaders={COLUMN_HEADERS}
            renderDataCells={renderPrefixDataCells}
            removedColumns={REMOVED_COLUMNS}
            itemNoun="prefix"
            emptyMessage="No prefixes match your search."
        />
    );
};
