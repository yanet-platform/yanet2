import React from 'react';
import { ConfirmModal } from './ConfirmModal';

interface DeleteConfigModalProps {
    open: boolean;
    configName: string;
    onClose: () => void;
    onConfirm: () => void;
    /** The noun used in the title and body. Defaults to "config". */
    noun?: string;
}

/** Modal confirming deletion of an entire config. */
const DeleteConfigModal: React.FC<DeleteConfigModalProps> = ({
    open,
    configName,
    onClose,
    onConfirm,
    noun = 'config',
}) => (
    <ConfirmModal
        open={open}
        title={`Delete ${noun}`}
        confirmText="Delete"
        onClose={onClose}
        onConfirm={onConfirm}
    >
        <p>Delete {noun} <code>{configName}</code>? This cannot be undone.</p>
    </ConfirmModal>
);

export default DeleteConfigModal;
