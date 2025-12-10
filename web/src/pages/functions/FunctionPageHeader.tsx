import React from 'react';
import { Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageHeader } from '../../components';

export interface FunctionPageHeaderProps {
    onCreateFunction: () => void;
}

export const FunctionPageHeader: React.FC<FunctionPageHeaderProps> = ({
    onCreateFunction,
}) => {
    return (
        <PageHeader
            title="Functions"
            actions={
                <Button view="action" onClick={onCreateFunction}>
                    <Button.Icon>
                        <Plus />
                    </Button.Icon>
                    Create function
                </Button>
            }
        />
    );
};
