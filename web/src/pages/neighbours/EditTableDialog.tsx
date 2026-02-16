import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import { useDialogKeyboardShortcut } from '../../hooks';
import type { EditTableDialogProps } from './types';

export const EditTableDialog: React.FC<EditTableDialogProps> = ({
    open,
    onClose,
    onConfirm,
    tableInfo,
}) => {
    const [defaultPriority, setDefaultPriority] = useState('');
    const [isSubmitting, setIsSubmitting] = useState(false);

    useEffect(() => {
        if (open && tableInfo) {
            setDefaultPriority(tableInfo.default_priority?.toString() || '0');
            setIsSubmitting(false);
        }
    }, [open, tableInfo]);

    const priorityNum = Number(defaultPriority);
    const priorityError = !defaultPriority.trim() || isNaN(priorityNum) || priorityNum < 0
        ? 'Priority must be a non-negative number'
        : undefined;

    const canSubmit = !isSubmitting && !priorityError;

    const handleConfirm = useCallback(async () => {
        if (!canSubmit || !tableInfo?.name) return;

        setIsSubmitting(true);
        try {
            await onConfirm(tableInfo.name, priorityNum);
            onClose();
        } finally {
            setIsSubmitting(false);
        }
    }, [canSubmit, tableInfo, priorityNum, onConfirm, onClose]);

    useDialogKeyboardShortcut({ open, canSubmit, onConfirm: handleConfirm });

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption={`Edit Table — ${tableInfo?.name || ''}`} />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                    <FormField label="Name" hint="Table name (read-only)">
                        <TextInput
                            value={tableInfo?.name || ''}
                            disabled
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
                            autoFocus
                        />
                    </FormField>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={onClose}
                textButtonApply="Save"
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

