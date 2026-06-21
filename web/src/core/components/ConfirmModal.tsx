import React from 'react';
import { useDialogKeyboardShortcut } from '../hooks';

export interface ConfirmModalProps {
    open: boolean;
    title: string;
    confirmText: string;
    onClose: () => void;
    onConfirm: () => void;
    children: React.ReactNode;
    /** Defaults to "Cancel". */
    cancelText?: string;
    /** When true the confirm button uses yn-btn--danger. Defaults to true. */
    danger?: boolean;
    /** When true both buttons are disabled and busyText (if provided) is shown. */
    busy?: boolean;
    /** Text shown on the confirm button while busy. Falls back to confirmText. */
    busyText?: string;
}

export const ConfirmModal: React.FC<ConfirmModalProps> = ({
    open,
    title,
    confirmText,
    onClose,
    onConfirm,
    children,
    cancelText = 'Cancel',
    danger = true,
    busy = false,
    busyText,
}) => {
    useDialogKeyboardShortcut({
        open,
        canSubmit: !busy,
        onConfirm,
        onCancel: busy ? undefined : onClose,
    });
    if (!open) return null;
    const confirmClass = `yn-btn ${danger ? 'yn-btn--danger' : 'yn-btn--primary'}`;
    return (
        <div className="yn-modal-backdrop" onClick={busy ? undefined : onClose}>
            <div className="yn-modal yn-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="yn-modal__head">
                    <span className="yn-modal__title">{title}</span>
                    <button type="button" className="yn-icon-btn" onClick={onClose} aria-label="Close">✕</button>
                </header>
                <div className="yn-modal__body yn-modal__body--confirm">
                    {children}
                </div>
                <footer className="yn-modal__foot">
                    <span />
                    <div className="yn-modal__foot-actions">
                        <button type="button" className="yn-btn yn-btn--ghost" disabled={busy} onClick={onClose}>
                            {cancelText}
                        </button>
                        <button type="button" className={confirmClass} disabled={busy} onClick={onConfirm}>
                            {busy ? (busyText ?? confirmText) : confirmText}
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default ConfirmModal;
