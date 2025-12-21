import React, { useState, useCallback, useEffect } from 'react';
import { Box, Dialog, TextInput } from '@gravity-ui/uikit';
import { FormField } from '../../components';
import './decap.css';

export interface AddConfigDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (configName: string) => void;
    existingConfigs: string[];
}

export const AddConfigDialog: React.FC<AddConfigDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingConfigs,
}) => {
    const [configName, setConfigName] = useState<string>('');

    const handleClose = useCallback(() => {
        setConfigName('');
        onClose();
    }, [onClose]);

    const handleConfirm = useCallback(() => {
        const trimmedName = configName.trim();
        if (trimmedName && !existingConfigs.includes(trimmedName)) {
            onConfirm(trimmedName);
            handleClose();
        }
    }, [configName, existingConfigs, onConfirm, handleClose]);

    const trimmedName = configName.trim();
    const isEmpty = trimmedName.length === 0;
    const isDuplicate = existingConfigs.includes(trimmedName);
    const hasError = !isEmpty && isDuplicate;
    const errorMessage = isDuplicate ? 'Config with this name already exists' : undefined;
    const canSubmit = !isEmpty && !hasError;

    // Handle Ctrl+Enter / Cmd+Enter
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
        <Dialog open={open} onClose={handleClose}>
            <Dialog.Header caption="Add Config" />
            <Dialog.Body>
                <Box className="decap-dialog__body">
                    <FormField
                        label="Config Name"
                        required
                        hint="Name for the new decap configuration. Press Ctrl+Enter to save."
                    >
                        <TextInput
                            value={configName}
                            onUpdate={setConfigName}
                            placeholder="Enter config name"
                            className="decap-dialog__text-input"
                            validationState={hasError ? 'invalid' : undefined}
                            errorMessage={errorMessage}
                            autoFocus
                        />
                    </FormField>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonApply={handleConfirm}
                onClickButtonCancel={handleClose}
                textButtonApply="Add"
                textButtonCancel="Cancel"
                propsButtonApply={{
                    view: 'action' as const,
                    disabled: !canSubmit,
                }}
            />
        </Dialog>
    );
};
