import { createContext, useContext } from 'react';

export const PAGE_IDS = ['inspect', 'functions', 'pipelines', 'devices', 'neighbours', 'route', 'forward', 'decap', 'pdump', 'acl'] as const;

export type PageId = typeof PAGE_IDS[number];

// Context for controlling sidebar disabled state
export interface SidebarContextValue {
    setSidebarDisabled: (disabled: boolean) => void;
}

export const SidebarContext = createContext<SidebarContextValue>({
    setSidebarDisabled: () => {},
});

export const useSidebarContext = (): SidebarContextValue => useContext(SidebarContext);
