import React from 'react';
import { CreateEntityDialog } from '../../../components';

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
    return (
        <CreateEntityDialog
            open={open}
            onClose={onClose}
            onConfirm={onConfirm}
            entityType="Pipeline"
        />
    );
};
