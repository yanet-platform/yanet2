import { useCallback, useState } from 'react';
import { MOCK_CONFIGS, createMockRouteGenerator } from './mockData';
import { toaster } from '../../utils';
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
            toaster.info('mock-mode-enabled', `Enabled with ${config.label}`, 'Mock Mode');
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
            toaster.info('mock-size-changed', `Changed to ${config.label}`, 'Mock Mode');
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
