import React, { useState, useCallback, useEffect } from 'react';
import { TextInput } from '@gravity-ui/uikit';
import { FormDialog } from './FormDialog';
import { FormField } from './FormField';
import './common.css';

export interface CreateEntityDialogProps {
    /** Whether the dialog is open */
    open: boolean;
    /** Handler for closing the dialog */
    onClose: () => void;
    /** Handler for confirming with the entered name */
    onConfirm: (name: string) => void;
    /** Entity type for display (e.g., "Function", "Pipeline", "Device") */
    entityType: string;
    /** Placeholder text for the input */
    placeholder?: string;
    /** Hint text shown below the input */
    hint?: string;
    /** Custom validation function. Returns error message or undefined if valid */
    validate?: (name: string) => string | undefined;
}

/**
 * Reusable dialog for creating entities with a name field
 */
export const CreateEntityDialog: React.FC<CreateEntityDialogProps> = ({
    open,
    onClose,
    onConfirm,
    entityType,
    placeholder,
    hint,
    validate,
}) => {
    const [name, setName] = useState('');
    const [error, setError] = useState<string | undefined>();

    // Reset form when dialog opens
    useEffect(() => {
        if (open) {
            setName('');
            setError(undefined);
        }
    }, [open]);

    const handleConfirm = useCallback(() => {
        const trimmedName = name.trim();
        if (!trimmedName) {
            setError(`${entityType} name is required`);
            return;
        }

        if (validate) {
            const validationError = validate(trimmedName);
            if (validationError) {
                setError(validationError);
                return;
            }
        }

        onConfirm(trimmedName);
    }, [name, entityType, validate, onConfirm]);

    const defaultPlaceholder = placeholder ?? `Enter ${entityType.toLowerCase()} name`;
    const defaultHint = hint ?? `Unique identifier for the ${entityType.toLowerCase()}`;

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title={`Create ${entityType}`}
            confirmText="Create"
        >
            <FormField
                label={`${entityType} Name`}
                required
                hint={defaultHint}
            >
                <TextInput
                    value={name}
                    onUpdate={setName}
                    placeholder={defaultPlaceholder}
                    className="create-entity-dialog__input"
                    validationState={error ? 'invalid' : undefined}
                    errorMessage={error}
                    autoFocus
                />
            </FormField>
        </FormDialog>
    );
};
