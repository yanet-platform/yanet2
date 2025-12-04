import React from 'react';
import { Flex, Box, Text, Divider } from '@gravity-ui/uikit';

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
        <Flex
            direction="column"
            style={{ flex: 1, width: '100%', minWidth: 0, height: '100%' }}
        >
            {(title || header) && (
                <>
                    <Box spacing={{ px: 5, py: 3 }} style={{ width: '100%', minHeight: '56px', display: 'flex', alignItems: 'center' }}>
                        {header || <Text variant="header-1">{title}</Text>}
                    </Box>
                    <Divider />
                </>
            )}
            <Flex
                direction="column"
                grow
                style={{ width: '100%', minWidth: 0, minHeight: 0, overflow: 'hidden' }}
            >
                {children}
            </Flex>
        </Flex>
    );
};
