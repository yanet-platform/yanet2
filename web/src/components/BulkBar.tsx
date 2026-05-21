import React from 'react';

interface BulkBarProps {
    /** Number of selected items. */
    count: number;
    /** Noun for the selected item type, e.g. "rule" or "route". Defaults to "item". */
    itemNoun?: string;
    onDelete: () => void;
    onClear: () => void;
}

/** Floating bulk-action bar that appears when items are selected. */
const BulkBar: React.FC<BulkBarProps> = ({ count, itemNoun = 'item', onDelete, onClear }) => (
    <div className="fw-bulk-bar">
        <span className="fw-bulk-bar__count">{count} selected</span>
        <button type="button" className="fw-btn fw-btn--danger fw-btn--sm" onClick={onDelete}>
            Delete {count} {itemNoun}{count !== 1 ? 's' : ''}
        </button>
        <button type="button" className="fw-icon-btn fw-icon-btn--sm" onClick={onClear} aria-label="Clear selection">
            ✕
        </button>
    </div>
);

export default BulkBar;
