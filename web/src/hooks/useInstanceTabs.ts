import { useCallback, useEffect, useState } from 'react';

export interface UseInstanceTabsOptions<T> {
    /** Array of items to create tabs for */
    items: T[];
    /** Function to get the instance index from an item */
    getInstanceIndex?: (item: T, idx: number) => number;
}

export interface UseInstanceTabsResult {
    /** Currently active tab value (string index) */
    activeTab: string;
    /** Handler for tab changes */
    setActiveTab: (tab: string) => void;
    /** Current tab index as number */
    currentTabIndex: number;
}

/**
 * Hook for managing instance tabs state with validation
 */
export const useInstanceTabs = <T>({
    items,
    getInstanceIndex,
}: UseInstanceTabsOptions<T>): UseInstanceTabsResult => {
    const [activeTab, setActiveTab] = useState<string>('0');

    // Validate and reset tab if necessary
    useEffect(() => {
        if (items.length > 0) {
            const tabExists = items.some((_, idx) => String(idx) === activeTab);
            if (!tabExists) {
                setActiveTab('0');
            }
        }
    }, [items, activeTab]);

    const currentTabIndex = parseInt(activeTab, 10);

    // Expose getInstanceIndex for convenience
    void getInstanceIndex;

    return {
        activeTab,
        setActiveTab,
        currentTabIndex,
    };
};

export interface UseNestedTabsOptions {
    /** Map of parent index to child tab value */
    initialTabs?: Map<number, string>;
}

export interface UseNestedTabsResult {
    /** Map of parent index to active child tab */
    activeConfigTab: Map<number, string>;
    /** Get active tab for a specific parent */
    getActiveTab: (parentIndex: number, defaultValue: string) => string;
    /** Set active tab for a specific parent */
    setActiveTab: (parentIndex: number, tab: string) => void;
    /** Set the entire map (for initialization) */
    setActiveConfigTab: React.Dispatch<React.SetStateAction<Map<number, string>>>;
}

/**
 * Hook for managing nested tabs (e.g., config tabs within instance tabs)
 */
export const useNestedTabs = ({
    initialTabs = new Map(),
}: UseNestedTabsOptions = {}): UseNestedTabsResult => {
    const [activeConfigTab, setActiveConfigTab] = useState<Map<number, string>>(initialTabs);

    const getActiveTab = useCallback((parentIndex: number, defaultValue: string): string => {
        return activeConfigTab.get(parentIndex) || defaultValue;
    }, [activeConfigTab]);

    const setActiveTab = useCallback((parentIndex: number, tab: string): void => {
        setActiveConfigTab(prev => {
            const newMap = new Map(prev);
            newMap.set(parentIndex, tab);
            return newMap;
        });
    }, []);

    return {
        activeConfigTab,
        getActiveTab,
        setActiveTab,
        setActiveConfigTab,
    };
};

