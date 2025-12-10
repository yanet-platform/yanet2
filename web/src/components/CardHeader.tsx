import React from 'react';
import { Text, Button, Label, Box } from '@gravity-ui/uikit';
import { FloppyDisk, TrashBin } from '@gravity-ui/icons';
import './CardHeader.css';

export interface CardHeaderProps {
    /** Card title */
    title: string;
    /** Whether there are unsaved changes */
    isDirty?: boolean;
    /** Whether this is a new entity */
    isNew?: boolean;
    /** Handler for save action */
    onSave: () => void;
    /** Handler for delete action */
    onDelete?: () => void;
    /** Whether save button is disabled */
    saveDisabled?: boolean;
    /** Whether save action is in progress */
    saving?: boolean;
    /** Additional labels to show after the title */
    labels?: React.ReactNode;
}

/**
 * Reusable card header with title, status indicators, and Save/Delete buttons
 */
export const CardHeader: React.FC<CardHeaderProps> = ({
    title,
    isDirty = false,
    isNew = false,
    onSave,
    onDelete,
    saveDisabled = false,
    saving = false,
    labels,
}) => {
    return (
        <Box className="card-header">
            <Box className="card-header__title-group">
                {labels}
                <Text variant="subheader-2">{title}</Text>
                {isNew && <Label theme="warning">new</Label>}
                {isDirty && !isNew && (
                    <Text variant="caption-1" color="secondary">
                        (unsaved changes)
                    </Text>
                )}
            </Box>
            <Box className="card-header__actions">
                <Button
                    view="action"
                    onClick={onSave}
                    disabled={saveDisabled || !isDirty}
                    loading={saving}
                >
                    <Button.Icon>
                        <FloppyDisk />
                    </Button.Icon>
                    Save
                </Button>
                {onDelete && (
                    <Button
                        view="outlined-danger"
                        onClick={onDelete}
                    >
                        <Button.Icon>
                            <TrashBin />
                        </Button.Icon>
                        Delete
                    </Button>
                )}
            </Box>
        </Box>
    );
};
