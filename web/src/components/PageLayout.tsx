import React from 'react';
import { Flex, Box, Text, Divider } from '@gravity-ui/uikit';
import './common.css';

export interface PageLayoutProps {
    /** Page title displayed in header. If not provided, header is not shown. */
    title?: string;
    /** Custom header content. If provided, overrides title. */
    header?: React.ReactNode;
    /** Main page content */
    children: React.ReactNode;
}

export const PageLayout = ({ title, header, children }: PageLayoutProps): React.JSX.Element => {
    return (
        <Flex direction="column" className="page-layout">
            {(title || header) && (
                <>
                    <Box spacing={{ px: 5, py: 3 }} className="page-layout__header">
                        {header || <Text variant="header-1">{title}</Text>}
                    </Box>
                    <Divider />
                </>
            )}
            <Flex direction="column" grow className="page-layout__content">
                {children}
            </Flex>
        </Flex>
    );
};
