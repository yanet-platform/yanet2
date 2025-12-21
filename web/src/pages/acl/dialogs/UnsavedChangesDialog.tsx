import React from 'react';
import { Box, Text, Dialog, Button } from '@gravity-ui/uikit';
import type { UnsavedChangesDialogProps } from '../types';
import '../acl.css';

export const UnsavedChangesDialog: React.FC<UnsavedChangesDialogProps> = ({
    open,
    onClose,
    onDiscard,
    onSave,
    configName,
}) => {
    return (
        <Dialog open={open} onClose={onClose}>
            <Dialog.Header caption="Unsaved Changes" />
            <Dialog.Body>
                <Box className="unsaved-dialog__body">
                    <Text variant="body-1">
                        You have unsaved changes in config "{configName}".
                    </Text>
                    <Text variant="body-2" color="secondary">
                        What would you like to do?
                    </Text>
                </Box>
            </Dialog.Body>
            <Dialog.Footer>
                <Box className="unsaved-dialog__footer">
                    <Button view="flat" onClick={onClose}>
                        Cancel
                    </Button>
                    <Button view="outlined-danger" onClick={onDiscard}>
                        Discard Changes
                    </Button>
                    <Button view="action" onClick={onSave}>
                        Save Changes
                    </Button>
                </Box>
            </Dialog.Footer>
        </Dialog>
    );
};
