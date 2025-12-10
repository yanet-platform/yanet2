import React from 'react';
import { CreateEntityDialog } from '../../../components';

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
    return (
        <CreateEntityDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            entityType="Function"
        />
    );
};
