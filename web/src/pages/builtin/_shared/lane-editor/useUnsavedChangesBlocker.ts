import { useEffect } from 'react';
import { useSidebarContext } from '../../../../types';

// TODO: also block react-router navigation (back/forward, useNavigate,
// redirect routes). Requires migrating App.tsx from <BrowserRouter> to
// createBrowserRouter + <RouterProvider> so that useBlocker becomes
// available. Tracked as a follow-up to verdict 1 of the builtin-pages
// review.

/**
 * Registers a beforeunload handler when hasUnsavedChanges is true, so the
 * browser shows its native "Leave page?" confirmation on tab close or hard
 * navigation. Also registers a sidebar navigation guard via SidebarContext so
 * that clicking a sidebar item while there are unsaved changes shows a
 * window.confirm prompt before navigating away.
 */
export const useUnsavedChangesBlocker = (hasUnsavedChanges: boolean): void => {
    const { setUnsavedGuard } = useSidebarContext();

    useEffect(() => {
        if (!hasUnsavedChanges) {
            setUnsavedGuard(null);
            return;
        }

        const handleBeforeUnload = (e: BeforeUnloadEvent): void => {
            e.preventDefault();
        };

        window.addEventListener('beforeunload', handleBeforeUnload);
        setUnsavedGuard(() => hasUnsavedChanges);

        return () => {
            window.removeEventListener('beforeunload', handleBeforeUnload);
            setUnsavedGuard(null);
        };
    }, [hasUnsavedChanges, setUnsavedGuard]);
};
