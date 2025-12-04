import { useCallback, useState } from 'react';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import { MOCK_CONFIGS, createMockRouteGenerator } from './mockData';
import type { MockRouteGenerator } from './mockData';

export interface UseMockModeResult {
    mockEnabled: boolean;
    mockSize: string;
    mockGenerator: MockRouteGenerator | null;
    mockSelectedIds: Set<string>;
    setMockSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>;
    handleMockToggle: (enabled: boolean) => void;
    handleMockSizeChange: (size: string) => void;
}

export const useMockMode = (): UseMockModeResult => {
    const [mockEnabled, setMockEnabled] = useState<boolean>(false);
    const [mockSize, setMockSize] = useState<string>('huge');
    const [mockGenerator, setMockGenerator] = useState<MockRouteGenerator | null>(null);
    const [mockSelectedIds, setMockSelectedIds] = useState<Set<string>>(new Set());

    const handleMockToggle = useCallback((enabled: boolean) => {
        setMockEnabled(enabled);
        if (enabled) {
            const config = MOCK_CONFIGS[mockSize as keyof typeof MOCK_CONFIGS];
            const generator = createMockRouteGenerator(config.routeCount);
            setMockGenerator(generator);
            setMockSelectedIds(new Set());
            toaster.add({
                name: 'mock-mode-enabled',
                title: 'Mock Mode',
                content: `Enabled with ${config.label}`,
                theme: 'info',
                isClosable: true,
                autoHiding: 3000,
            });
        } else {
            setMockGenerator(null);
            setMockSelectedIds(new Set());
        }
    }, [mockSize]);

    const handleMockSizeChange = useCallback((size: string) => {
        setMockSize(size);
        if (mockEnabled) {
            const config = MOCK_CONFIGS[size as keyof typeof MOCK_CONFIGS];
            const generator = createMockRouteGenerator(config.routeCount);
            setMockGenerator(generator);
            setMockSelectedIds(new Set());
            toaster.add({
                name: 'mock-size-changed',
                title: 'Mock Mode',
                content: `Changed to ${config.label}`,
                theme: 'info',
                isClosable: true,
                autoHiding: 3000,
            });
        }
    }, [mockEnabled]);

    return {
        mockEnabled,
        mockSize,
        mockGenerator,
        mockSelectedIds,
        setMockSelectedIds,
        handleMockToggle,
        handleMockSizeChange,
    };
};

