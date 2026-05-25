import React from 'react';
import PdumpModal from './PdumpModal';
import './pdump.scss';

interface DeleteConfigDialogProps {
    name: string;
    isDeleting: boolean;
    onClose: () => void;
    onConfirm: () => void;
}

/** Pdump-specific delete confirmation dialog. */
const DeleteConfigDialog: React.FC<DeleteConfigDialogProps> = ({ name, isDeleting, onClose, onConfirm }) => {
    const footer = (
        <>
            <button type="button" className="fw-btn fw-btn--ghost" onClick={onClose} disabled={isDeleting}>
                Cancel
            </button>
            <button
                type="button"
                className="fw-btn pdump-modal-btn--danger"
                onClick={onConfirm}
                disabled={isDeleting}
            >
                {isDeleting ? 'Deleting…' : 'Delete'}
            </button>
        </>
    );

    return (
        <PdumpModal title="Delete Pdump Configuration" width="460px" onClose={onClose} footer={footer}>
            <p className="pdump-modal__delete-text">
                Delete configuration <code className="pdump-modal__code">{name}</code>?
            </p>
            <p className="pdump-modal__warning">This action cannot be undone.</p>
        </PdumpModal>
    );
};

export default DeleteConfigDialog;
