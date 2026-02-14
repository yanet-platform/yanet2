import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import type { CreateTableDialogProps } from './types';

export const CreateTableDialog: React.FC<CreateTableDialogProps> = ({
    open,
    onClose,
    onConfirm,
}) => {
    const [name, setName] = useState('');
    const [defaultPriority, setDefaultPriority] = useState('100');
    const [isSubmitting, setIsSubmitting] = useState(false);

    useEffect(() => {
        if (open) {
            setName('');
            setDefaultPriority('100');
            setIsSubmitting(false);
        }
    }, [open]);

    const nameError = name.trim() ? undefined : 'Name is required';
    const priorityNum = Number(defaultPriority);
    const priorityError = !defaultPriority.trim() || isNaN(priorityNum) || priorityNum < 0
        ? 'Priority must be a non-negative number'
        : undefined;

    const canSubmit = !isSubmitting && !nameError && !priorityError;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit) return;

        setIsSubmitting(true);
        try {
            await onConfirm(name.trim(), priorityNum);
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [canSubmit, name, priorityNum, onConfirm, onClose]);

    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter' && canSubmit) {
                e.preventDefault();
                handleConfirm();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => document.removeEventListener('keydown', handleKeyDown);
    }, [open, canSubmit, handleConfirm]);

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Create Neighbour Table" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                    <FormField label="Name" required hint="Unique table name">
                        <TextInput
                            value={name}
                            onUpdate={setName}
                            placeholder="e.g., my-table"
                            validationState={name && nameError ? 'invalid' : undefined}
                            errorMessage={nameError}
                            autoFocus
                        />
                    </FormField>

                    <FormField label="Default Priority" required hint="Default priority for new entries (lower = higher priority)">
                        <TextInput
                            value={defaultPriority}
                            onUpdate={setDefaultPriority}
                            placeholder="e.g., 100"
                            type="number"
                            validationState={defaultPriority && priorityError ? 'invalid' : undefined}
                            errorMessage={priorityError}
                        />
                    </FormField>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Create"
                textButtonCancel="Cancel"
                propsButtonApply={{
                    view: 'action' as const,
                    disabled: !canSubmit,
                    loading: isSubmitting,
                }}
            />
        </Dialog>
    );
};

