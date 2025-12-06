import React from 'react';
import { Flex, Text, Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';

export interface PipelinePageHeaderProps {
    onCreatePipeline: () => void;
}

export const PipelinePageHeader: React.FC<PipelinePageHeaderProps> = ({
    onCreatePipeline,
}) => {
    return (
        <Flex
            alignItems="center"
            justifyContent="space-between"
            style={{ width: '100%' }}
        >
            <Text variant="header-1">Pipelines</Text>
            <Button view="action" onClick={onCreatePipeline}>
                <Button.Icon>
                    <Plus />
                </Button.Icon>
                Create pipeline
            </Button>
        </Flex>
    );
};

