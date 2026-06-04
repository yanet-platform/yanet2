import React from 'react';

interface Props {
    filtered: number;
    total: number;
}

/** Displays a filtered/total row count in the toolbar, e.g. "42 / 1 000". */
const RowCountDisplay = ({ filtered, total }: Props): React.JSX.Element => (
    <span className="yn-count">
        <span style={{ color: 'var(--yn-text)', fontWeight: 600 }}>{filtered.toLocaleString()}</span>
        {' / '}{total.toLocaleString()}
    </span>
);

export default RowCountDisplay;
