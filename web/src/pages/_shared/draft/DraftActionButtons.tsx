import React from 'react';
import { Icon } from '@gravity-ui/uikit';
import { ArrowUturnCcwLeft } from '@gravity-ui/icons';

/** Save / floppy disk icon. */
export const SaveIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 5h11l3 3v11H5zM8 5v5h7V5M8 14h8v5H8z" />
    </svg>
);

/** Trash / delete icon. */
export const TrashIcon = (): React.JSX.Element => (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M5 7h14M9 7V5h6v2M7 7l1 12h8l1-12" />
    </svg>
);

interface DraftActionButtonsProps {
    currentIsDirty: boolean;
    onSave: () => void;
    onDiscard: () => void;
    onDeleteConfig: () => void;
}

/** Right-side action strip with discard / save / delete-config buttons. */
const DraftActionButtons: React.FC<DraftActionButtonsProps> = ({
    currentIsDirty,
    onSave,
    onDiscard,
    onDeleteConfig,
}) => (
    <div className="fw-tbl-actions">
        {currentIsDirty && (
            <button
                type="button"
                className="fw-tbl-action-btn fw-tbl-action-btn--discard"
                title="Discard changes"
                aria-label="Discard local changes"
                onClick={onDiscard}
            >
                <Icon data={ArrowUturnCcwLeft} size={16} />
            </button>
        )}
        <button
            type="button"
            className="fw-tbl-action-btn fw-tbl-action-btn--save"
            title={currentIsDirty ? 'Review & apply' : 'No changes to save'}
            aria-label="Review and apply changes"
            disabled={!currentIsDirty}
            onClick={onSave}
        >
            <SaveIcon />
        </button>
        <button
            type="button"
            className="fw-tbl-action-btn fw-tbl-action-btn--delete"
            title="Delete config"
            aria-label="Delete config"
            onClick={onDeleteConfig}
        >
            <TrashIcon />
        </button>
    </div>
);

export default DraftActionButtons;
