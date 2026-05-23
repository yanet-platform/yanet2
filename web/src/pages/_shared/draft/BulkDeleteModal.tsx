import React from 'react';

interface BulkDeleteModalProps {
    open: boolean;
    count: number;
    itemNoun: string;
    configName: string;
    onClose: () => void;
    onConfirm: () => void;
    /** Live-write pages (operators/*) set this to switch the hint sentence to a non-draft wording. */
    immediate?: boolean;
}

/** Modal confirming bulk deletion of selected rows. */
const BulkDeleteModal: React.FC<BulkDeleteModalProps> = ({
    open,
    count,
    itemNoun,
    configName,
    onClose,
    onConfirm,
    immediate = false,
}) => {
    if (!open) return null;
    return (
        <div className="fw-modal-backdrop" onClick={onClose}>
            <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="fw-modal__head">
                    <span className="fw-modal__title">Delete {itemNoun}s</span>
                    <button type="button" className="fw-icon-btn" onClick={onClose} aria-label="Close">✕</button>
                </header>
                <div className="fw-modal__body fw-modal__body--confirm">
                    <p>
                        Delete <strong>{count}</strong> selected {itemNoun}(s) from <code>{configName}</code>?
                        {' '}
                        {immediate
                            ? 'This action cannot be undone.'
                            : 'Changes are staged in the draft; discard the draft to revert.'}
                    </p>
                </div>
                <footer className="fw-modal__foot">
                    <span />
                    <div className="fw-modal__foot-actions">
                        <button type="button" className="fw-btn fw-btn--ghost" onClick={onClose}>Cancel</button>
                        <button type="button" className="fw-btn fw-btn--danger" onClick={onConfirm}>
                            Delete {count} {itemNoun}(s)
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default BulkDeleteModal;
