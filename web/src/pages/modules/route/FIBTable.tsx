import React, { useMemo } from 'react';
import type { FIBRowItem, FIBRowStatus } from './types';
import { validateRow } from './validation';
import { VirtualDraftTable, LEADING_TOTAL_WIDTH } from '../../_shared/draft';
import type { RemovedColumnDescriptor, TableColumnHeader } from '../../_shared/draft';

const COLUMN_WIDTHS = {
    prefix: 220,
    dst_mac: 180,
    src_mac: 180,
    device: 120,
} as const;

const TOTAL_WIDTH =
    LEADING_TOTAL_WIDTH +
    COLUMN_WIDTHS.prefix + COLUMN_WIDTHS.dst_mac + COLUMN_WIDTHS.src_mac + COLUMN_WIDTHS.device;

const COLUMN_HEADERS: TableColumnHeader[] = [
    { width: COLUMN_WIDTHS.prefix, label: 'Prefix' },
    { width: COLUMN_WIDTHS.dst_mac, label: 'Dst MAC' },
    { width: COLUMN_WIDTHS.src_mac, label: 'Src MAC' },
    { width: COLUMN_WIDTHS.device, label: 'Device' },
];

const REMOVED_COLUMNS: RemovedColumnDescriptor<FIBRowItem>[] = [
    { width: COLUMN_WIDTHS.prefix, render: (r) => <span className="fw-cell-mono">{r.prefix}</span> },
    { width: COLUMN_WIDTHS.dst_mac, render: (r) => <span className="fw-cell-mono fw-cell-muted">{r.dst_mac}</span> },
    { width: COLUMN_WIDTHS.src_mac, render: (r) => <span className="fw-cell-mono fw-cell-muted">{r.src_mac}</span> },
    { width: COLUMN_WIDTHS.device, render: (r) => <span className="fw-cell-mono fw-cell-muted">{r.device}</span> },
];

const dataCellStyle = (width: number, hasError: boolean): React.CSSProperties => ({
    width,
    minWidth: width,
    maxWidth: width,
    flexShrink: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    paddingRight: 8,
    display: 'flex',
    alignItems: 'center',
    ...(hasError ? { color: 'var(--fw-danger)' } : {}),
});

const renderFIBDataCells = (row: FIBRowItem): React.ReactNode => {
    const errors = validateRow(row);
    return (
        <>
            <div style={dataCellStyle(COLUMN_WIDTHS.prefix, !!errors.prefix)} title={row.prefix || undefined}>
                <span className="fw-cell-mono fw-cell-strong">
                    {row.prefix || <span style={{ color: 'var(--fw-text-3)', fontStyle: 'italic' }}>prefix?</span>}
                </span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.dst_mac, !!errors.dst_mac)} title={row.dst_mac || undefined}>
                <span className="fw-cell-mono fw-cell-muted">{row.dst_mac || '—'}</span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.src_mac, !!errors.src_mac)} title={row.src_mac || undefined}>
                <span className="fw-cell-mono fw-cell-muted">{row.src_mac || '—'}</span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.device, !!errors.device)} title={row.device || undefined}>
                <span className="fw-cell-mono fw-cell-muted">{row.device || '—'}</span>
            </div>
        </>
    );
};

export interface FIBTableProps {
    allRows: FIBRowItem[];
    visibleRows: FIBRowItem[];
    statusById: Map<string, FIBRowStatus>;
    removedRows: FIBRowItem[];
    activeRowId: string | null;
    editingRowId: string | null;
    selectedIds: Set<string>;
    dragOverState: { id: string | null; where: 'top' | 'bottom' | null };
    onRowClick: (id: string) => void;
    onEditRow: (id: string) => void;
    onRestoreRow: (row: FIBRowItem) => void;
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

/** Virtualized FIB table backed by VirtualDraftTable. */
export const FIBTable: React.FC<FIBTableProps> = (props) => {
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
            renderDataCells={renderFIBDataCells}
            removedColumns={REMOVED_COLUMNS}
            itemNoun="route"
            emptyMessage="No routes match your search."
        />
    );
};
