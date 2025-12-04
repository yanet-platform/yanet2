import React, { useEffect, useState } from 'react';
import { Flex, Loader, LoaderProps } from '@gravity-ui/uikit';

export interface PageLoaderProps extends LoaderProps {
    /** Whether the loader should be shown */
    loading: boolean;
    /** Delay in milliseconds before showing the loader. Default: 200ms */
    delay?: number;
}

/**
 * PageLoader component displays a centered loader that fills the available container.
 * The loader is shown only if loading state persists longer than the specified delay.
 * 
 * @example
 * ```tsx
 * <PageLoader loading={isLoading} size="l" delay={200} />
 * ```
 */
export const PageLoader = ({ loading, size = 'l', delay = 200 }: PageLoaderProps): React.JSX.Element => {
    const [showLoader, setShowLoader] = useState(false);

    useEffect(() => {
        if (!loading) {
            setShowLoader(false);
            return;
        }

        let timeoutId: number | null = null;

        // Show loader only if loading persists longer than delay
        timeoutId = window.setTimeout(() => {
            setShowLoader(true);
        }, delay);

        return () => {
            if (timeoutId) {
                clearTimeout(timeoutId);
            }
        };
    }, [loading, delay]);

    if (!showLoader) {
        return <></>;
    }

    return (
        <Flex
            alignItems="center"
            justifyContent="center"
            spacing={{ p: 5 }}
            style={{ flex: 1 }}
        >
            <Loader size={size} />
        </Flex>
    );
};
