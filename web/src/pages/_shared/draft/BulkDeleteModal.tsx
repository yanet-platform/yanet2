import React from 'react';
import { useDialogKeyboardShortcut } from '../../../hooks';

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
    useDialogKeyboardShortcut({ open, canSubmit: true, onConfirm, onCancel: onClose });
    if (!open) return null;
    return (
        <div className="yn-modal-backdrop" onClick={onClose}>
            <div className="yn-modal yn-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="yn-modal__head">
                    <span className="yn-modal__title">Delete {itemNoun}s</span>
                    <button type="button" className="yn-icon-btn" onClick={onClose} aria-label="Close">✕</button>
                </header>
                <div className="yn-modal__body yn-modal__body--confirm">
                    <p>
                        Delete <strong>{count}</strong> selected {itemNoun}(s) from <code>{configName}</code>?
                        {' '}
                        {immediate
                            ? 'This action cannot be undone.'
                            : 'Changes are staged in the draft; discard the draft to revert.'}
                    </p>
                </div>
                <footer className="yn-modal__foot">
                    <span />
                    <div className="yn-modal__foot-actions">
                        <button type="button" className="yn-btn yn-btn--ghost" onClick={onClose}>Cancel</button>
                        <button type="button" className="yn-btn yn-btn--danger" onClick={onConfirm}>
                            Delete {count} {itemNoun}(s)
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default BulkDeleteModal;
