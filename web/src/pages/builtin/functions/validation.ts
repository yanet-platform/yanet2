import type { NetworkFunction } from './types';

/** Validate a single function, returning error messages (editing-time only; weight=0 is allowed). */
export const validateFn = (fn: NetworkFunction): string[] => {
    const errors: string[] = [];
    for (const chain of fn.chains) {
        const names = new Set<string>();
        for (const m of chain.modules) {
            if (names.has(m.name)) {
                errors.push(`Chain "${chain.name}": duplicate module name "${m.name}"`);
            }
            names.add(m.name);
        }
    }
    return errors;
};

/** Check save-time constraints (weight sum > 0 required). */
export const validateSave = (fn: NetworkFunction): string[] => {
    const errors: string[] = [];
    const totalWeight = fn.chains.reduce((s, c) => s + c.weight, 0);
    if (totalWeight === 0 && fn.chains.length > 0) {
        errors.push('Total chain weight is 0 — at least one chain must have weight > 0 before saving.');
    }
    return errors;
};

/** Returns true if a function passes all editing- and save-time validation. */
export const isFnSaveable = (fn: NetworkFunction): boolean =>
    validateFn(fn).length === 0 && validateSave(fn).length === 0;
