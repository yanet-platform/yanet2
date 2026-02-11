import React, { useState, useCallback, useEffect } from 'react';
import { Box, TextInput, Select, Text } from '@gravity-ui/uikit';
import { FormDialog } from '../../../components';
import { DEVICE_TYPES, type DeviceType } from '../../../api/devices';
import '../devices.scss';

export interface CreateDeviceDialogProps {
    open: boolean;
    onClose: () => void;
    onConfirm: (name: string, type: DeviceType) => void;
    existingNames?: string[];
}

export const CreateDeviceDialog: React.FC<CreateDeviceDialogProps> = ({
    open,
    onClose,
    onConfirm,
    existingNames = [],
}) => {
    const [name, setName] = useState('');
    const [type, setType] = useState<DeviceType>('plain');
    const [error, setError] = useState<string | null>(null);

    // Reset form when dialog opens
    useEffect(() => {
        if (open) {
            setName('');
            setType('plain');
            setError(null);
        }
    }, [open]);

    const validate = useCallback((): boolean => {
        if (!name.trim()) {
            setError('Device name is required');
            return false;
        }
        if (existingNames.includes(name.trim())) {
            setError('Device with this name already exists');
            return false;
        }
        setError(null);
        return true;
    }, [name, existingNames]);

    const handleConfirm = useCallback(() => {
        if (validate()) {
            onConfirm(name.trim(), type);
            onClose();
        }
    }, [validate, name, type, onConfirm, onClose]);

    const handleNameChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        setName(e.target.value);
        setError(null);
    }, []);

    const handleTypeChange = useCallback((value: string[]) => {
        if (value.length > 0 && DEVICE_TYPES.includes(value[0] as DeviceType)) {
            setType(value[0] as DeviceType);
        }
    }, []);

    const typeOptions = DEVICE_TYPES.map(t => ({
        value: t,
        content: t,
    }));

    return (
        <FormDialog
            open={open}
            onClose={onClose}
            onConfirm={handleConfirm}
            title="Create Device"
            confirmText="Create"
        >
            <Box className="create-device-dialog__body">
                <Box>
                    <Text variant="body-1" className="create-device-dialog__label">
                        Name
                    </Text>
                    <TextInput
                        value={name}
                        onChange={handleNameChange}
                        placeholder="Enter device name"
                        autoFocus
                        error={!!error}
                        errorMessage={error ?? undefined}
                    />
                </Box>
                <Box>
                    <Text variant="body-1" className="create-device-dialog__label">
                        Type
                    </Text>
                    <Select
                        value={[type]}
                        options={typeOptions}
                        onUpdate={handleTypeChange}
                        width="max"
                    />
                </Box>
            </Box>
        </FormDialog>
    );
};
