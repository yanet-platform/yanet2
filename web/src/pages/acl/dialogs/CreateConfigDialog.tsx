import React, { useState, useCallback, useEffect } from 'react';
import { TextInput } from '@gravity-ui/uikit';
import { FormDialog } from '../../../components/FormDialog';
import { FormField } from '../../../components/FormField';
import type { CreateConfigDialogProps } from '../types';

export const CreateConfigDialog: React.FC<CreateConfigDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingConfigs,
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

    const validate = useCallback((value: string): string | undefined => {
        const trimmed = value.trim();
        if (!trimmed) {
            return 'Config name is required';
        }
        if (existingConfigs.includes(trimmed)) {
            return 'A config with this name already exists';
        }
        return undefined;
    }, [existingConfigs]);

    const handleNameChange = useCallback((value: string) => {
        setName(value);
        setError(validate(value));
    }, [validate]);

    const handleConfirm = useCallback(() => {
        const validationError = validate(name);
        if (validationError) {
            setError(validationError);
            return;
        }
        onConfirm(name.trim());
    }, [name, validate, onConfirm]);

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Create ACL Config"
            confirmText="Create"
        >
            <FormField
                label="Config Name"
                required
                hint="Unique identifier for the ACL configuration"
            >
                <TextInput
                    value={name}
                    onUpdate={handleNameChange}
                    placeholder="Enter config name"
                    validationState={error ? 'invalid' : undefined}
                    errorMessage={error}
                    autoFocus
                />
            </FormField>
        </FormDialog>
    );
};
