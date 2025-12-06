import React, { useState, useCallback, useEffect } from 'react';
import { TextInput } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';

export interface CreatePipelineDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (name: string) => void;
}

export const CreatePipelineDialog: React.FC<CreatePipelineDialogProps> = ({
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
            setError('Pipeline name is required');
            return;
        }
        onConfirm(trimmedName);
    }, [name, onConfirm]);
    
    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Create Pipeline"
            confirmText="Create"
        >
            <FormField
                label="Pipeline Name"
                required
                hint="Unique identifier for the pipeline"
            >
                <TextInput
                    value={name}
                    onUpdate={setName}
                    placeholder="Enter pipeline name"
                    style={{ width: '100%' }}
                    validationState={error ? 'invalid' : undefined}
                    errorMessage={error}
                    autoFocus
                />
            </FormField>
        </FormDialog>
    );
};

