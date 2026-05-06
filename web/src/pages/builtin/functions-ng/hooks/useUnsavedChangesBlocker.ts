import { useEffect } from 'react';

/**
 * Registers a beforeunload handler when hasUnsavedChanges is true, so the
 * browser shows its native "Leave page?" confirmation on tab close or
 * hard navigation. In-app route navigation is not blocked because the app
 * uses BrowserRouter (non-data router) and useBlocker requires a data router.
 */
export const useUnsavedChangesBlocker = (hasUnsavedChanges: boolean): void => {
    useEffect(() => {
        if (!hasUnsavedChanges) {
            return;
        }

        const handleBeforeUnload = (e: BeforeUnloadEvent): void => {
            e.preventDefault();
        };

        window.addEventListener('beforeunload', handleBeforeUnload);
        return () => {
            window.removeEventListener('beforeunload', handleBeforeUnload);
        };
    }, [hasUnsavedChanges]);
};
