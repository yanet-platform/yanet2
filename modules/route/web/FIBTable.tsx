import React, { useMemo } from 'react';
import type { FIBRowItem } from './types';
import { validateRow } from './validation';
import { VirtualDraftTable, LEADING_TOTAL_WIDTH } from '@yanet/core/components/VirtualTable';
import type { RemovedColumnDescriptor, TableColumnHeader, RowStatus, VirtualDraftTableBaseProps } from '@yanet/core/components/VirtualTable';
import { DraftActionButtons } from '@yanet/core/components/draft';

const COLUMN_WIDTHS = {
    from: 170,
    to: 170,
    dst_mac: 180,
    src_mac: 180,
    device: 120,
    counter: 160,
} as const;

const TOTAL_WIDTH =
    LEADING_TOTAL_WIDTH +
    COLUMN_WIDTHS.from + COLUMN_WIDTHS.to + COLUMN_WIDTHS.dst_mac + COLUMN_WIDTHS.src_mac +
    COLUMN_WIDTHS.device + COLUMN_WIDTHS.counter;

const COLUMN_HEADERS: TableColumnHeader[] = [
    { width: COLUMN_WIDTHS.from, label: 'From' },
    { width: COLUMN_WIDTHS.to, label: 'To' },
    { width: COLUMN_WIDTHS.dst_mac, label: 'Dst MAC' },
    { width: COLUMN_WIDTHS.src_mac, label: 'Src MAC' },
    { width: COLUMN_WIDTHS.device, label: 'Device' },
    { width: COLUMN_WIDTHS.counter, label: 'Counter' },
];

const REMOVED_COLUMNS: RemovedColumnDescriptor<FIBRowItem>[] = [
    { width: COLUMN_WIDTHS.from, render: (r) => <span className="yn-cell-mono">{r.from}</span> },
    { width: COLUMN_WIDTHS.to, render: (r) => <span className="yn-cell-mono">{r.to}</span> },
    { width: COLUMN_WIDTHS.dst_mac, render: (r) => <span className="yn-cell-mono yn-cell-muted">{r.dst_mac}</span> },
    { width: COLUMN_WIDTHS.src_mac, render: (r) => <span className="yn-cell-mono yn-cell-muted">{r.src_mac}</span> },
    { width: COLUMN_WIDTHS.device, render: (r) => <span className="yn-cell-mono yn-cell-muted">{r.device}</span> },
    { width: COLUMN_WIDTHS.counter, render: (r) => <span className="yn-cell-mono yn-cell-muted">{r.counter}</span> },
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
    ...(hasError ? { color: 'var(--yn-danger)' } : {}),
});

const renderFIBDataCells = (row: FIBRowItem): React.ReactNode => {
    const errors = validateRow(row);
    return (
        <>
            <div style={dataCellStyle(COLUMN_WIDTHS.from, !!errors.range)} title={row.from || undefined}>
                <span className="yn-cell-mono yn-cell-strong">
                    {row.from || <span style={{ color: 'var(--yn-text-3)', fontStyle: 'italic' }}>from?</span>}
                </span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.to, !!errors.range)} title={row.to || undefined}>
                <span className="yn-cell-mono yn-cell-strong">
                    {row.to || <span style={{ color: 'var(--yn-text-3)', fontStyle: 'italic' }}>to?</span>}
                </span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.dst_mac, !!errors.dst_mac)} title={row.dst_mac || undefined}>
                <span className="yn-cell-mono yn-cell-muted">{row.dst_mac || '—'}</span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.src_mac, !!errors.src_mac)} title={row.src_mac || undefined}>
                <span className="yn-cell-mono yn-cell-muted">{row.src_mac || '—'}</span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.device, !!errors.device)} title={row.device || undefined}>
                <span className="yn-cell-mono yn-cell-muted">{row.device || '—'}</span>
            </div>
            <div style={dataCellStyle(COLUMN_WIDTHS.counter, !!errors.counter)} title={row.counter || undefined}>
                <span className="yn-cell-mono yn-cell-muted">{row.counter || '—'}</span>
            </div>
        </>
    );
};

export type FIBTableProps = VirtualDraftTableBaseProps<FIBRowItem>;

/** Virtualized FIB table backed by VirtualDraftTable. */
export const FIBTable: React.FC<FIBTableProps> = (props) => {
    const statusById = useMemo(
        () => props.statusById as Map<string, RowStatus>,
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
            emptyMessage="No routes match your search."
            flushFooter
            headerActions={
                <DraftActionButtons
                    currentIsDirty={props.currentIsDirty}
                    onSave={props.onSave}
                    onDiscard={props.onDiscard}
                    onDeleteConfig={props.onDeleteConfig}
                />
            }
        />
    );
};
