import React from 'react';
import { Flex, Text, Box, Button } from '@gravity-ui/uikit';
import { Plus, FloppyDisk, TrashBin } from '@gravity-ui/icons';
import type { AclPageHeaderProps } from './types';

export const AclPageHeader: React.FC<AclPageHeaderProps> = ({
    onUploadYaml,
    onSave,
    onDeleteConfig,
    isSaveDisabled,
    isDeleteDisabled,
    hasUnsavedChanges,
    isSaving,
}) => (
    <Flex style={{ width: '100%', alignItems: 'center' }}>
        <Flex alignItems="center" gap={2}>
            <Text variant="header-1">ACL</Text>
            {hasUnsavedChanges && (
                <Box
                    style={{
                        width: 8,
                        height: 8,
                        borderRadius: '50%',
                        backgroundColor: 'var(--g-color-text-warning)',
                    }}
                />
            )}
        </Flex>
        <Box style={{ flex: 1 }} />
        <Box style={{ display: 'flex', gap: '12px', alignItems: 'center' }}>
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
