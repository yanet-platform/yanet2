import React from 'react';
import { ConfirmDialog } from './ConfirmDialog';

interface EntityConfirmDialogsProps {
    noun: string;
    entityId: string;
    deleteOpen: boolean;
    discardOpen: boolean;
    onDeleteClose: () => void;
    onDiscardClose: () => void;
    onDelete: () => void;
    onDiscard: () => void;
}

/** Renders the standard delete and discard confirmation dialogs for a named entity. */
export const EntityConfirmDialogs: React.FC<EntityConfirmDialogsProps> = ({
    noun,
    entityId,
    deleteOpen,
    discardOpen,
    onDeleteClose,
    onDiscardClose,
    onDelete,
    onDiscard,
}) => (
    <>
        <ConfirmDialog
            open={deleteOpen}
            onClose={onDeleteClose}
            onConfirm={() => { onDeleteClose(); onDelete(); }}
            title={`Delete ${noun}`}
            message={`Delete ${noun} "${entityId}"? This cannot be undone.`}
            confirmText="Delete"
            cancelText="Cancel"
            danger
        />
        <ConfirmDialog
            open={discardOpen}
            onClose={onDiscardClose}
            onConfirm={() => { onDiscardClose(); onDiscard(); }}
            title={`Discard changes to "${entityId}"?`}
            message={`All local edits to this ${noun} will be discarded.`}
            confirmText="Discard"
            cancelText="Cancel"
            danger
        />
    </>
);
