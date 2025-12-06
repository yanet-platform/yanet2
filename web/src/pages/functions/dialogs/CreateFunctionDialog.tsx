import React, { useState, useCallback, useEffect } from 'react';
import { TextInput } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';

export interface CreateFunctionDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (name: string) => void;
}

export const CreateFunctionDialog: React.FC<CreateFunctionDialogProps> = ({
    open,
    onClose,
    onConfirm,
}) => {
    const [name, setName] = useState('');
    const [error, setError] = useState<string | undefined>();
    
    useEffect(() => {
        if (open) {
            setName('');
            setError(undefined);
        }
    }, [open]);
    
    const handleConfirm = useCallback(() => {
        const trimmedName = name.trim();
        if (!trimmedName) {
            setError('Function name is required');
            return;
        }
        onConfirm(trimmedName);
    }, [name, onConfirm]);
    
    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Create Function"
            confirmText="Create"
        >
            <FormField
                label="Function Name"
                required
                hint="Unique identifier for the function"
            >
                <TextInput
                    value={name}
                    onUpdate={setName}
                    placeholder="Enter function name"
                    style={{ width: '100%' }}
                    validationState={error ? 'invalid' : undefined}
                    errorMessage={error}
                    autoFocus
                />
            </FormField>
        </FormDialog>
    );
};
