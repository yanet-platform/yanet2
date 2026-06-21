import React from 'react';
import { ConfirmModal } from '@yanet/core/components';

interface DeleteConfigDialogProps {
    name: string;
    isDeleting: boolean;
    onClose: () => void;
    onConfirm: () => void;
}

/** Pdump delete confirmation dialog. */
const DeleteConfigDialog: React.FC<DeleteConfigDialogProps> = ({ name, isDeleting, onClose, onConfirm }) => (
    <ConfirmModal
        open
        title="Delete Pdump Configuration"
        confirmText="Delete"
        busyText="Deleting…"
        busy={isDeleting}
        onClose={onClose}
        onConfirm={onConfirm}
    >
        <p>Delete configuration <code>{name}</code>?</p>
        <p>This action cannot be undone.</p>
    </ConfirmModal>
);

export default DeleteConfigDialog;
