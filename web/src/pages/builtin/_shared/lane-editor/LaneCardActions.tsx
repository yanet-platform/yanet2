import React from 'react';
import { TrashIcon, SaveIcon, DiscardIcon } from '../icons';

interface LaneCardActionsProps {
    prefix: 'fn' | 'pl';
    isDirty: boolean;
    saveDisabled: boolean;
    deleteTitle: string;
    deleteAriaLabel: string;
    onDiscard: () => void;
    onOpenDiff: () => void;
    onDelete: () => void;
}

export const LaneCardActions = ({
    prefix,
    isDirty,
    saveDisabled,
    deleteTitle,
    deleteAriaLabel,
    onDiscard,
    onOpenDiff,
    onDelete,
}: LaneCardActionsProps): React.JSX.Element => (
    <div className={`${prefix}-card-header__actions`}>
        {isDirty && (
            <button
                className={`${prefix}-card-header__icon-btn ${prefix}-card-header__icon-btn--discard`}
                type="button"
                title="Discard changes"
                aria-label="Discard local changes"
                onClick={onDiscard}
            >
                <DiscardIcon />
            </button>
        )}
        <button
            className={`${prefix}-card-header__icon-btn ${prefix}-card-header__icon-btn--save`}
            onClick={onOpenDiff}
            disabled={saveDisabled}
            type="button"
            title={isDirty ? 'Review & apply' : 'No changes to save'}
            aria-label="Review and apply changes"
        >
            <SaveIcon />
        </button>
        <button
            className={`${prefix}-card-header__icon-btn ${prefix}-card-header__icon-btn--delete`}
            onClick={onDelete}
            type="button"
            title={deleteTitle}
            aria-label={deleteAriaLabel}
        >
            <TrashIcon size={18} />
        </button>
    </div>
);
