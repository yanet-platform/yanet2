import { useCallback } from 'react';
import { toaster } from '../../utils';

/** Action type discriminants used by both ACL and Forward reducers. */
export type ConfigPersistenceActionType =
    | 'MARK_SAVED'
    | 'DELETE_CONFIG'
    | 'DISCARD_CONFIG'
    | 'CANCEL_PENDING_DELETE';

/**
 * Minimal dispatch shape shared by both ACL and Forward reducers.
 *
 * Both action unions include MARK_SAVED, DELETE_CONFIG, DISCARD_CONFIG, and
 * (for Forward) CANCEL_PENDING_DELETE. The cast at each call site is required
 * by parameter contravariance: the concrete reducer dispatch accepts a wider
 * union, while this type constrains callers to the four known action types.
 */
export type ConfigPersistenceDispatch = (action: {
    type: ConfigPersistenceActionType;
    configName: string;
}) => void;

export interface UseConfigPersistenceOptions<R> {
    /** Persist one config's rules to the server. Module-level stable reference required. */
    updateConfig: (name: string, rules: R[]) => Promise<unknown>;
    /** Delete one config from the server. Module-level stable reference required. */
    deleteConfig: (name: string) => Promise<unknown>;
    /** Prefix for toaster keys, e.g. 'acl-save' or 'yn-save'. */
    toastKeyPrefix: string;
    /** Action type dispatched on commitDeleteConfig failure (rollback divergence). */
    rollbackActionType: 'DISCARD_CONFIG' | 'CANCEL_PENDING_DELETE';
    /** Stable rawDispatch from useReducer. */
    rawDispatch: ConfigPersistenceDispatch;
    /** Current draft rule sets, keyed by config name. */
    draft: Record<string, R[]>;
    /** Set of config names currently marked for deletion on save. */
    pendingDeleteConfigs: Set<string>;
    /** Config names that exist only locally and have never been saved. */
    localOnlyConfigs: string[];
}

export interface UseConfigPersistenceResult {
    /** Save one config to the server, respecting any pending-delete flag. */
    saveConfig: (configName: string) => Promise<void>;
    /** Immediately commit a delete: dispatches DELETE_CONFIG, calls the API for server configs. */
    commitDeleteConfig: (configName: string) => Promise<void>;
    /** Revert one config's draft back to the server snapshot. */
    discardConfig: (configName: string) => void;
}

/**
 * Shared config-persistence callbacks for draft-based module pages.
 *
 * Encapsulates the saveConfig / commitDeleteConfig / discardConfig trio that is
 * otherwise duplicated between the ACL and Forward draft hooks. The only
 * semantic divergence — which action to dispatch when a delete fails — is
 * parameterised via rollbackActionType.
 *
 * Identity stability: saveConfig changes when draft or pendingDeleteConfigs
 * change (same deps as before extraction). commitDeleteConfig changes when
 * localOnlyConfigs changes. discardConfig is always stable. This matches the
 * original per-hook deps exactly.
 *
 * Callers MUST supply updateConfig and deleteConfig as module-level stable
 * references (defined outside any React function) so that their identity does
 * not churn on every render and invalidate saveConfig / commitDeleteConfig
 * unnecessarily.
 */
export const useConfigPersistence = <R>(
    options: UseConfigPersistenceOptions<R>,
): UseConfigPersistenceResult => {
    const {
        updateConfig,
        deleteConfig,
        toastKeyPrefix,
        rollbackActionType,
        rawDispatch,
        draft,
        pendingDeleteConfigs,
        localOnlyConfigs,
    } = options;

    const saveConfig = useCallback(async (configName: string): Promise<void> => {
        const isPendingDelete = pendingDeleteConfigs.has(configName);

        if (isPendingDelete) {
            try {
                await deleteConfig(configName);
                rawDispatch({ type: 'MARK_SAVED', configName });
                toaster.success(`${toastKeyPrefix}-${configName}`, `Config "${configName}" deleted.`);
            } catch (err) {
                toaster.error(`${toastKeyPrefix}-err-${configName}`, `Failed to delete "${configName}"`, err);
                throw err;
            }
            return;
        }

        const rules = draft[configName] ?? [];
        try {
            await updateConfig(configName, rules);
            rawDispatch({ type: 'MARK_SAVED', configName });
            toaster.success(`${toastKeyPrefix}-${configName}`, `Config "${configName}" saved.`);
        } catch (err) {
            toaster.error(`${toastKeyPrefix}-err-${configName}`, `Failed to save "${configName}"`, err);
            throw err;
        }
    }, [draft, pendingDeleteConfigs, deleteConfig, updateConfig, toastKeyPrefix, rawDispatch]);

    const commitDeleteConfig = useCallback(async (configName: string): Promise<void> => {
        const isLocalOnly = localOnlyConfigs.includes(configName);
        rawDispatch({ type: 'DELETE_CONFIG', configName });
        if (isLocalOnly) {
            return;
        }
        try {
            await deleteConfig(configName);
            rawDispatch({ type: 'MARK_SAVED', configName });
            toaster.success(`${toastKeyPrefix}-${configName}`, `Config "${configName}" deleted.`);
        } catch (err) {
            rawDispatch({ type: rollbackActionType, configName });
            toaster.error(`${toastKeyPrefix}-err-${configName}`, `Failed to delete "${configName}"`, err);
            throw err;
        }
    }, [localOnlyConfigs, deleteConfig, toastKeyPrefix, rollbackActionType, rawDispatch]);

    const discardConfig = useCallback((configName: string): void => {
        rawDispatch({ type: 'DISCARD_CONFIG', configName });
    }, [rawDispatch]);

    return { saveConfig, commitDeleteConfig, discardConfig };
};
