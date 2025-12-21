import React from 'react';
import { Flex, Text, Box, Button } from '@gravity-ui/uikit';
import { Plus, FloppyDisk, TrashBin } from '@gravity-ui/icons';
import type { AclPageHeaderProps } from './types';
import './acl.css';

export const AclPageHeader: React.FC<AclPageHeaderProps> = ({
    onUploadYaml,
    onSave,
    onDeleteConfig,
    isSaveDisabled,
    isDeleteDisabled,
    hasUnsavedChanges,
    isSaving,
}) => (
    <Flex className="acl-header">
        <Flex alignItems="center" gap={2}>
            <Text variant="header-1">ACL</Text>
            {hasUnsavedChanges && <Box className="acl-header__unsaved-indicator" />}
        </Flex>
        <Box className="acl-header__spacer" />
        <Box className="acl-header__actions">
            <Button view="action" onClick={onUploadYaml} disabled={isSaving}>
                <Button.Icon>
                    <Plus />
                </Button.Icon>
                Create Config
            </Button>
            <Button
                view="outlined"
                onClick={onSave}
                disabled={isSaveDisabled || isSaving}
                loading={isSaving}
            >
                <Button.Icon>
                    <FloppyDisk />
                </Button.Icon>
                Save
            </Button>
            <Button
                view="outlined-danger"
                onClick={onDeleteConfig}
                disabled={isDeleteDisabled || isSaving}
            >
                <Button.Icon>
                    <TrashBin />
                </Button.Icon>
                Delete Config
            </Button>
        </Box>
    </Flex>
);
