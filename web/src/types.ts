import { createContext, useContext } from 'react';

export const PAGE_IDS = [
    'builtin/inspect',
    'builtin/functions',
    'builtin/functions-ng',
    'builtin/pipelines',
    'builtin/devices',
    'modules/forward',
    'modules/decap',
    'modules/acl',
    'modules/pdump',
    'modules/route',
    'operators/route',
    'operators/neighbours',
] as const;

export type PageId = typeof PAGE_IDS[number];

// Context for controlling sidebar disabled state and unsaved-changes navigation guard.
export interface SidebarContextValue {
    setSidebarDisabled: (disabled: boolean) => void;
    /** Register a predicate that returns true when there are unsaved changes. Pass null to clear. */
    setUnsavedGuard: (hasUnsaved: (() => boolean) | null) => void;
}

export const SidebarContext = createContext<SidebarContextValue>({
    setSidebarDisabled: () => {},
    setUnsavedGuard: () => {},
});

export const useSidebarContext = (): SidebarContextValue => useContext(SidebarContext);
