import React, { useState } from 'react';

interface AddConfigModalProps {
    open: boolean;
    onClose: () => void;
    onCreate: (name: string) => void;
    title?: string;
    placeholder?: string;
    existingNames: string[];
    label?: string;
}

/** Modal for creating a new named config — mirrors the inline addConfigOpen block in RoutePage. */
const AddConfigModal: React.FC<AddConfigModalProps> = ({
    open,
    onClose,
    onCreate,
    title = 'Add config',
    placeholder = 'e.g. default',
    existingNames,
    label = 'Config name',
}) => {
    const [name, setName] = useState('');

    if (!open) return null;

    const trimmed = name.trim();
    const isDisabled = !trimmed || existingNames.includes(trimmed);

    const handleCreate = (): void => {
        if (isDisabled) return;
        onCreate(trimmed);
        setName('');
    };

    const handleClose = (): void => {
        setName('');
        onClose();
    };

    return (
        <div className="fw-modal-backdrop" onClick={handleClose}>
            <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="fw-modal__head">
                    <span className="fw-modal__title">{title}</span>
                    <button type="button" className="fw-icon-btn" onClick={handleClose} aria-label="Close">✕</button>
                </header>
                <div className="fw-modal__body fw-modal__body--confirm">
                    <div className="fw-field">
                        <label className="fw-field__label" htmlFor="draft-new-config-name">
                            {label} <span className="fw-field__req">*</span>
                        </label>
                        <input
                            id="draft-new-config-name"
                            className="fw-input"
                            type="text"
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') handleCreate();
                                if (e.key === 'Escape') handleClose();
                            }}
                            placeholder={placeholder}
                            autoFocus
                        />
                    </div>
                </div>
                <footer className="fw-modal__foot">
                    <span />
                    <div className="fw-modal__foot-actions">
                        <button type="button" className="fw-btn fw-btn--ghost" onClick={handleClose}>Cancel</button>
                        <button
                            type="button"
                            className="fw-btn fw-btn--primary"
                            onClick={handleCreate}
                            disabled={isDisabled}
                        >
                            Create
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default AddConfigModal;
