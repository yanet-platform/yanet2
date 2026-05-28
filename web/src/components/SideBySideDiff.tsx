import React, { useMemo } from 'react';
import type { Change } from 'diff';

/** One row in the side-by-side diff: left content (removed/context), right content (added/context). */
export interface SbsRow {
    left: string | null;
    right: string | null;
    kind: 'added' | 'removed' | 'context';
    rowIdx: number;
}

/** Build side-by-side rows from a list of unified diff changes. */
export const buildSbsRows = (changes: Change[]): SbsRow[] => {
    const rows: SbsRow[] = [];
    let rowIdx = 0;

    for (const change of changes) {
        const lines = change.value.split('\n');
        const trimmed = lines[lines.length - 1] === '' ? lines.slice(0, -1) : lines;

        if (change.removed) {
            for (const line of trimmed) {
                rows.push({ left: line, right: null, kind: 'removed', rowIdx: rowIdx++ });
            }
        } else if (change.added) {
            for (const line of trimmed) {
                rows.push({ left: null, right: line, kind: 'added', rowIdx: rowIdx++ });
            }
        } else {
            for (const line of trimmed) {
                rows.push({ left: line, right: line, kind: 'context', rowIdx: rowIdx++ });
            }
        }
    }

    return rows;
};

const MONO_FONT = 'ui-monospace, monospace';

const CELL_STYLE: React.CSSProperties = {
    flex: 1,
    minWidth: 0,
    overflowX: 'auto',
    fontFamily: MONO_FONT,
    fontSize: 12,
    lineHeight: '1.6',
    whiteSpace: 'pre',
    padding: '0 12px',
    userSelect: 'text',
};

const ROW_STYLE: React.CSSProperties = {
    display: 'flex',
    minHeight: '1.6em',
    borderBottom: '1px solid transparent',
};

const COLUMN_HEADER_STYLE: React.CSSProperties = {
    padding: '2px 12px',
    background: 'var(--g-color-base-generic)',
    borderBottom: '1px solid var(--g-color-line-generic)',
    fontSize: 11,
    color: 'var(--g-color-text-hint)',
    fontWeight: 600,
    letterSpacing: '0.3px',
    textTransform: 'uppercase',
};

export const getRowBg = (kind: SbsRow['kind'], side: 'left' | 'right'): string => {
    if (kind === 'removed' && side === 'left') {
        return 'color-mix(in srgb, var(--g-color-text-danger) 10%, transparent)';
    }
    if (kind === 'added' && side === 'right') {
        return 'color-mix(in srgb, var(--g-color-text-positive) 10%, transparent)';
    }
    return 'transparent';
};

export const getTextColor = (kind: SbsRow['kind'], side: 'left' | 'right'): string => {
    if (kind === 'removed' && side === 'left') {
        return 'var(--g-color-text-danger)';
    }
    if (kind === 'added' && side === 'right') {
        return 'var(--g-color-text-positive)';
    }
    if (kind === 'context') {
        return 'var(--g-color-text-secondary)';
    }
    return 'var(--g-color-text-hint)';
};

/** Side-by-side diff view rendering server state on the left and local state on the right. */
export const SideBySideDiff = ({ changes }: { changes: Change[] }): React.JSX.Element => {
    const rows = useMemo(() => buildSbsRows(changes), [changes]);

    return (
        <div style={{
            display: 'flex',
            fontFamily: MONO_FONT,
            fontSize: 12,
            lineHeight: '1.6',
            border: '1px solid var(--g-color-line-generic)',
            borderRadius: 4,
            overflow: 'hidden',
        }}>
            <div style={{ flex: 1, minWidth: 0, borderRight: '1px solid var(--g-color-line-generic)' }}>
                <div style={COLUMN_HEADER_STYLE}>Before (server)</div>
                {rows.map(row => (
                    <div
                        key={`l-${row.rowIdx}`}
                        style={{ ...ROW_STYLE, background: getRowBg(row.kind, 'left') }}
                    >
                        <span style={{ ...CELL_STYLE, color: getTextColor(row.kind, 'left') }}>
                            {row.left ?? ''}
                        </span>
                    </div>
                ))}
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
                <div style={COLUMN_HEADER_STYLE}>After (local)</div>
                {rows.map(row => (
                    <div
                        key={`r-${row.rowIdx}`}
                        style={{ ...ROW_STYLE, background: getRowBg(row.kind, 'right') }}
                    >
                        <span style={{ ...CELL_STYLE, color: getTextColor(row.kind, 'right') }}>
                            {row.right ?? ''}
                        </span>
                    </div>
                ))}
            </div>
        </div>
    );
};
