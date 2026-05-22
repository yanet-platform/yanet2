import React from 'react';

const RESTORE_ICON = (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" />
        <path d="M3 3v5h5" />
    </svg>
);

/** Descriptor for a single data column in the removed-rows ghost section. */
export interface RemovedColumnDescriptor<T> {
    /** Fixed pixel width of the column (must match the live table column width). */
    width: number;
    /** Render the cell content for a removed row. */
    render: (row: T) => React.ReactNode;
}

/** Fixed widths for the four leading structural cells. */
interface LeadingWidths {
    checkbox: number;
    handle: number;
    index: number;
    status: number;
}

interface RemovedRowsSectionProps<T extends { id: string }> {
    rows: T[];
    rowHeight: number;
    totalWidth: number;
    leadingWidths: LeadingWidths;
    columns: RemovedColumnDescriptor<T>[];
    onRestore: (row: T) => void;
}

const leadingCellStyle = (width: number): React.CSSProperties => ({
    width,
    minWidth: width,
    maxWidth: width,
    flexShrink: 0,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
});

const dataCellStyle = (width: number): React.CSSProperties => ({
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
    justifyContent: 'flex-start',
});

/** Ghost section that renders server-only rows that were removed from the draft. */
const RemovedRowsSection = <T extends { id: string }>({
    rows,
    rowHeight,
    totalWidth,
    leadingWidths,
    columns,
    onRestore,
}: RemovedRowsSectionProps<T>): React.JSX.Element | null => {
    if (rows.length === 0) return null;

    return (
        <div style={{ minWidth: totalWidth }}>
            {rows.map((r) => (
                <div
                    key={r.id}
                    className="fw-vrow"
                    style={{
                        display: 'flex',
                        alignItems: 'center',
                        height: rowHeight,
                        minWidth: totalWidth,
                        borderBottom: '1px solid var(--fw-line)',
                        paddingLeft: 4,
                        opacity: 0.55,
                        background: 'rgba(224, 122, 110, 0.04)',
                        position: 'relative',
                    }}
                >
                    <div style={leadingCellStyle(leadingWidths.checkbox)} />
                    <div style={leadingCellStyle(leadingWidths.handle)}>
                        <span style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', color: 'var(--fw-danger)', fontSize: 11 }}>✕</span>
                    </div>
                    <div style={leadingCellStyle(leadingWidths.index)}>
                        <span style={{ fontSize: 12, color: 'var(--fw-text-3)' }}>—</span>
                    </div>
                    <div style={leadingCellStyle(leadingWidths.status)}>
                        <span className="fw-status-dot fw-status-dot--removed" title="Removed in draft" />
                    </div>
                    {columns.map((col, idx) => (
                        <div key={idx} style={dataCellStyle(col.width)}>
                            <span style={{ textDecoration: 'line-through', textDecorationColor: 'var(--fw-danger)' }}>
                                {col.render(r)}
                            </span>
                        </div>
                    ))}
                    <div style={{ marginLeft: 'auto', paddingRight: 8 }}>
                        <button
                            type="button"
                            className="fw-row-edit-btn fw-row-edit-btn--visible"
                            onClick={() => onRestore(r)}
                            title="Restore row"
                            aria-label="Restore removed row"
                            style={{ fontSize: 12 }}
                        >
                            {RESTORE_ICON}
                        </button>
                    </div>
                </div>
            ))}
        </div>
    );
};

export default RemovedRowsSection;
