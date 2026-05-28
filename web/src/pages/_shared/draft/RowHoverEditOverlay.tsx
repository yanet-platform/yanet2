import React from 'react';
import { TrashIcon } from './DraftActionButtons';

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
}) => (
    <div
        className={`fw-row-action-slot${onDelete ? ' fw-row-action-slot--wide' : ''}`}
        style={{ top, height: rowHeight }}
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
    >
        <button
            type="button"
            className="fw-row-edit-btn fw-row-edit-btn--visible"
            onClick={onEdit}
            aria-label={editAriaLabel}
            title={editTitle}
        >
            ✎
        </button>
        {onDelete && (
            <button
                type="button"
                className="fw-row-edit-btn fw-row-edit-btn--visible fw-row-edit-btn--danger"
                onClick={onDelete}
                aria-label={deleteAriaLabel ?? 'Delete'}
                title={deleteTitle ?? 'Delete'}
            >
                <TrashIcon />
            </button>
        )}
    </div>
);

export default RowHoverEditOverlay;
