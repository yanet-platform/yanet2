import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageHeader } from '../../components';

export interface PdumpPageHeaderProps {
    onCreateConfig: () => void;
}

export const PdumpPageHeader: React.FC<PdumpPageHeaderProps> = ({
    onCreateConfig,
}) => {
    return (
        <PageHeader
            title="Pdump"
            actions={
                <Button view="action" onClick={onCreateConfig}>
                    <Button.Icon>
                        <Plus />
                    </Button.Icon>
                    New Configuration
                </Button>
            }
        />
    );
};

