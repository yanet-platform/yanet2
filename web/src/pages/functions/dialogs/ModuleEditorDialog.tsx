import React, { useState, useCallback, useEffect } from 'react';
import { TextInput, Box } from '@gravity-ui/uikit';
import { FormDialog, FormField } from '../../../components';
import type { ModuleNodeData } from '../types';
import '../../FunctionsPage.css';

export interface ModuleEditorDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (data: ModuleNodeData) => void;
    initialData: ModuleNodeData;
}

export const ModuleEditorDialog: React.FC<ModuleEditorDialogProps> = ({
    open,
    onClose,
    onConfirm,
    initialData,
}) => {
    const [type, setType] = useState('');
    const [name, setName] = useState('');
    
    useEffect(() => {
        if (open) {
            setType(initialData.type || '');
            setName(initialData.name || '');
        }
    }, [open, initialData]);
    
    const handleConfirm = useCallback(() => {
        onConfirm({
            type: type.trim(),
            name: name.trim(),
        });
    }, [type, name, onConfirm]);
    
    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Edit Module"
            confirmText="Save"
            showCancel={false}
        >
            <Box className="module-editor-dialog__body">
                <FormField
                    label="Module Type"
                    hint="Type of the module (e.g., filter, nat, route)"
                >
                    <TextInput
                        value={type}
                        onUpdate={setType}
                        placeholder="Enter module type"
                        className="module-editor-dialog__input"
                        autoFocus
                    />
                </FormField>
                
                <FormField
                    label="Module Name"
                    hint="Instance name of the module"
                >
                    <TextInput
                        value={name}
                        onUpdate={setName}
                        placeholder="Enter module name"
                        className="module-editor-dialog__input"
                    />
                </FormField>
            </Box>
        </FormDialog>
    );
};
