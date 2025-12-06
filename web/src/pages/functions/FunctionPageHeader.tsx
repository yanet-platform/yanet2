import React from 'react';
import { Flex, Text, Button } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';

export interface FunctionPageHeaderProps {
    onCreateFunction: () => void;
}

export const FunctionPageHeader: React.FC<FunctionPageHeaderProps> = ({
    onCreateFunction,
}) => {
    return (
        <Flex
            alignItems="center"
            justifyContent="space-between"
            style={{ width: '100%' }}
        >
            <Text variant="header-1">Functions</Text>
            <Button view="action" onClick={onCreateFunction}>
                <Button.Icon>
                    <Plus />
                </Button.Icon>
                Create function
            </Button>
        </Flex>
    );
};

