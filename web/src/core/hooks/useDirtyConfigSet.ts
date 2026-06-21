import { useMemo } from 'react';

/** Returns the set of config names that currently have unsaved draft changes. */
export const useDirtyConfigSet = (
    draftConfigs: string[],
    isDirty: (config: string) => boolean,
): Set<string> =>
    useMemo(() => {
        const s = new Set<string>();
        draftConfigs.forEach((c) => {
            if (isDirty(c)) {
                s.add(c);
            }
        });
        return s;
    }, [draftConfigs, isDirty]);
