import React, { useEffect, useState } from 'react';

export interface CreateTableModalProps {
    open: boolean;
    onClose: () => void;
    onCreate: (name: string, defaultPriority: number) => Promise<void>;
    existingNames: string[];
}

/** Modal for creating a new neighbour table with a name and default priority. */
const CreateTableModal: React.FC<CreateTableModalProps> = ({
    open,
    onClose,
    onCreate,
    existingNames,
}) => {
    const [name, setName] = useState('');
    const [defaultPriority, setDefaultPriority] = useState('100');
    const [submitting, setSubmitting] = useState(false);

    useEffect(() => {
        if (open) {
            setName('');
            setDefaultPriority('100');
            setSubmitting(false);
        }
    }, [open]);

    if (!open) return null;

    const trimmedName = name.trim();
    const priorityNum = Number(defaultPriority);
    const nameError = submitting
        ? undefined
        : !trimmedName
            ? 'Name is required'
            : existingNames.includes(trimmedName)
                ? 'A table with this name already exists'
                : undefined;
    const priorityError =
        !defaultPriority.trim() || isNaN(priorityNum) || priorityNum < 0 || !Number.isInteger(priorityNum)
            ? 'Priority must be a non-negative integer'
            : undefined;
    const canCreate = !submitting && !nameError && !priorityError;

    const handleCreate = async (): Promise<void> => {
        if (!canCreate) return;
        setSubmitting(true);
        try {
            await onCreate(trimmedName, priorityNum);
            onClose();
        } catch {
            setSubmitting(false);
        }
    };

    const handleClose = (): void => {
        if (submitting) return;
        onClose();
    };

    return (
        <div className="fw-modal-backdrop" onClick={handleClose}>
            <div className="fw-modal fw-modal--sm" onClick={(e) => e.stopPropagation()}>
                <header className="fw-modal__head">
                    <span className="fw-modal__title">Create neighbour table</span>
                    <button type="button" className="fw-icon-btn" onClick={handleClose} aria-label="Close">✕</button>
                </header>
                <div className="fw-modal__body fw-modal__body--confirm">
                    <div className="fw-field">
                        <label className="fw-field__label" htmlFor="ct-name">
                            Name <span className="fw-field__req">*</span>
                        </label>
                        <input
                            id="ct-name"
                            className={`fw-input${nameError && trimmedName ? ' fw-input--invalid' : ''}`}
                            type="text"
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') handleCreate();
                                if (e.key === 'Escape') handleClose();
                            }}
                            placeholder="e.g. my-table"
                            autoFocus
                        />
                        {nameError && trimmedName && (
                            <span className="fw-field__hint fw-field__error">{nameError}</span>
                        )}
                    </div>
                    <div className="fw-field">
                        <label className="fw-field__label" htmlFor="ct-priority">
                            Default Priority <span className="fw-field__req">*</span>
                        </label>
                        <input
                            id="ct-priority"
                            className={`fw-input${priorityError && defaultPriority ? ' fw-input--invalid' : ''}`}
                            type="number"
                            value={defaultPriority}
                            onChange={(e) => setDefaultPriority(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') handleCreate();
                                if (e.key === 'Escape') handleClose();
                            }}
                            placeholder="100"
                        />
                        {priorityError && defaultPriority && (
                            <span className="fw-field__hint fw-field__error">{priorityError}</span>
                        )}
                    </div>
                </div>
                <footer className="fw-modal__foot">
                    <span />
                    <div className="fw-modal__foot-actions">
                        <button type="button" className="fw-btn fw-btn--ghost" onClick={handleClose} disabled={submitting}>
                            Cancel
                        </button>
                        <button
                            type="button"
                            className="fw-btn fw-btn--primary"
                            onClick={handleCreate}
                            disabled={!canCreate}
                        >
                            {submitting ? 'Creating…' : 'Create'}
                        </button>
                    </div>
                </footer>
            </div>
        </div>
    );
};

export default CreateTableModal;
