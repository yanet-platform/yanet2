import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageHeader } from '../../components';

export interface PipelinePageHeaderProps {
    onCreatePipeline: () => void;
}

export const PipelinePageHeader: React.FC<PipelinePageHeaderProps> = ({
    onCreatePipeline,
}) => {
    return (
        <PageHeader
            title="Pipelines"
            actions={
                <Button view="action" onClick={onCreatePipeline}>
                    <Button.Icon>
                        <Plus />
                    </Button.Icon>
                    Create pipeline
                </Button>
            }
        />
    );
};
