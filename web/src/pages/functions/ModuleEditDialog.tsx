import React, { useCallback, useEffect, useState } from 'react';
import { Box, Text, Dialog, TextInput } from '@gravity-ui/uikit';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import type { ModuleId } from '../../api/functions';

export interface ModuleEditDialogProps {
    open: boolean;
    onClose: () => void;
    moduleId: ModuleId | undefined;
    onSave: (moduleId: ModuleId) => void;
}

export const ModuleEditDialog: React.FC<ModuleEditDialogProps> = ({
    open,
    onClose,
    moduleId,
    onSave,
}) => {
    const [type, setType] = useState(moduleId?.type || '');
    const [name, setName] = useState(moduleId?.name || '');

    useEffect(() => {
        if (open) {
            setType(moduleId?.type || '');
            setName(moduleId?.name || '');
        }
    }, [open, moduleId]);

    const handleSave = useCallback(() => {
        if (!type.trim() || !name.trim()) {
            toaster.add({
                name: 'validation-error',
                title: 'Validation Error',
                content: 'Both type and name are required',
                theme: 'warning',
                isClosable: true,
                autoHiding: 3000,
            });
            return;
        }

        onSave({ type: type.trim(), name: name.trim() });
        onClose();
    }, [name, onClose, onSave, type]);

    useEffect(() => {
        if (!open) return;

        const handleKeyDown = (event: KeyboardEvent) => {
            if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') {
                event.preventDefault();
                handleSave();
            }
        };

        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [handleSave, open]);

    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Edit Module" />
            <Dialog.Body>
                <Box style={{ display: 'flex', flexDirection: 'column', gap: '16px', minWidth: '300px' }}>
                    <Box>
                        <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
                            Module Type
                        </Text>
                        <TextInput
                            value={type}
                            onUpdate={setType}
                            placeholder="e.g., route, acl, balancer"
                            autoFocus
                        />
                    </Box>
                    <Box>
                        <Text variant="body-1" style={{ marginBottom: '8px', display: 'block' }}>
                            Module Name
                        </Text>
                        <TextInput
                            value={name}
                            onUpdate={setName}
                            placeholder="e.g., main, default"
                        />
                    </Box>
                    <Text variant="body-1" color="secondary">
                        Type refers to the module kind (route, acl, etc.). Name is the configuration name.
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer
                onClickButtonCancel={onClose}
                onClickButtonApply={handleSave}
                textButtonApply="Save"
                textButtonCancel="Cancel"
            />
        </Dialog>
    );
};

