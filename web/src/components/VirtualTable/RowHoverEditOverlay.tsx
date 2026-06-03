import React from 'react';

/** Trash / delete icon. */
const TrashIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 7h14M9 7V5h6v2M7 7l1 12h8l1-12" />
    </svg>
);

interface RowHoverEditOverlayProps {
    /** Top offset in px from useRowHoverOverlay's overlayTopOffset. */
    top: number;
    /** Pixel height of one row. Sets the slot height so the button visually centers on the row. */
    rowHeight: number;
    /** Fired when the user clicks the Edit button. */
    onEdit: () => void;
    /** Accessible label for screen readers, e.g. "Edit rule 12" or "Edit route 192.0.2.0/24". */
    editAriaLabel: string;
    /** Hover title attribute, e.g. "Edit rule". */
    editTitle: string;
    /** Optional delete action. When provided, renders a second danger button to the right of edit. */
    onDelete?: () => void;
    /** Accessible label for the delete button. */
    deleteAriaLabel?: string;
    /** Hover title for the delete button. */
    deleteTitle?: string;
    /** Forwarded to the slot root so hover state can be tracked. */
    onMouseEnter: () => void;
    onMouseLeave: () => void;
    /** Custom edit button icon. Defaults to the ✎ glyph. */
    editIcon?: React.ReactNode;
    /** Custom delete button icon. Defaults to TrashIcon. */
    deleteIcon?: React.ReactNode;
}

/** Absolute-positioned edit button overlay that appears when a table row is hovered. */
const RowHoverEditOverlay: React.FC<RowHoverEditOverlayProps> = ({
    top,
    rowHeight,
    onEdit,
    editAriaLabel,
    editTitle,
    onDelete,
    deleteAriaLabel,
    deleteTitle,
    onMouseEnter,
    onMouseLeave,
    editIcon,
    deleteIcon,
}) => (
    <div
        className={`yn-row-action-slot${onDelete ? ' yn-row-action-slot--wide' : ''}`}
        style={{ top, height: rowHeight }}
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
    >
        <button
            type="button"
            className="yn-row-edit-btn yn-row-edit-btn--visible"
            onClick={onEdit}
            aria-label={editAriaLabel}
            title={editTitle}
        >
            {editIcon ?? '✎'}
        </button>
        {onDelete && (
            <button
                type="button"
                className="yn-row-edit-btn yn-row-edit-btn--visible yn-row-edit-btn--danger"
                onClick={onDelete}
                aria-label={deleteAriaLabel ?? 'Delete'}
                title={deleteTitle ?? 'Delete'}
            >
                {deleteIcon ?? <TrashIcon />}
            </button>
        )}
    </div>
);

export default RowHoverEditOverlay;
