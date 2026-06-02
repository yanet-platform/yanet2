import React from 'react';

interface DeleteConfigModalProps {
    open: boolean;
    configName: string;
    onClose: () => void;
    onConfirm: () => void;
}

/** Modal confirming deletion of an entire config. */
const DeleteConfigModal: React.FC<DeleteConfigModalProps> = ({
    open,
    configName,
    onClose,
    onConfirm,
}) => {
    if (!open) return null;
    return (
        <div className="yn-modal-backdrop" onClick={onClose}>
            <div className="yn-modal yn-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="yn-modal__head">
                    <span className="yn-modal__title">Delete config</span>
                    <button type="button" className="yn-icon-btn" onClick={onClose} aria-label="Close">✕</button>
                </header>
                <div className="yn-modal__body yn-modal__body--confirm">
                    <p>Delete config <code>{configName}</code>? This cannot be undone.</p>
                </div>
                <footer className="yn-modal__foot">
                    <span />
                    <div className="yn-modal__foot-actions">
                        <button type="button" className="yn-btn yn-btn--ghost" onClick={onClose}>Cancel</button>
                        <button type="button" className="yn-btn yn-btn--danger" onClick={onConfirm}>
                            Delete
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default DeleteConfigModal;
