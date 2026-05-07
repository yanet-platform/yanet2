import { useEffect } from 'react';
import { useSidebarContext } from '../../../../types';

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
