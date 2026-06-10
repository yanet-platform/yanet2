import React from 'react';

interface RuleDrawerHeadActionsProps {
    /** When true, renders the duplicate and delete buttons. */
    showEditActions: boolean;
    onDuplicate: () => void;
    onDelete: () => void;
    /** Icon or text rendered inside the delete button. */
    deleteIcon: React.ReactNode;
    onClose: () => void;
}

/** Head-action row shared by ACL and Forward rule drawers. */
const RuleDrawerHeadActions: React.FC<RuleDrawerHeadActionsProps> = ({
    showEditActions,
    onDuplicate,
    onDelete,
    deleteIcon,
    onClose,
}) => (
    <>
        {showEditActions && (
            <>
                <button
                    type="button"
                    className="yn-icon-btn"
                    onClick={onDuplicate}
                    title="Duplicate rule"
                >
                    ⎘
                </button>
                <button
                    type="button"
                    className="yn-icon-btn yn-icon-btn--danger"
                    onClick={onDelete}
                    title="Delete rule"
                >
                    {deleteIcon}
                </button>
            </>
        )}
        <button
            type="button"
            className="yn-icon-btn"
            onClick={onClose}
            aria-label="Close drawer"
        >
            ✕
        </button>
    </>
);

export default RuleDrawerHeadActions;
