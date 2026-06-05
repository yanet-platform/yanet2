import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Icon } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, EmptyState, CommandPaletteHeader } from '../../../components';
import { useFunctionsData } from './hooks/useFunctionsData';
import { useDragState, useUnsavedChangesBlocker } from '../_shared/lane-editor';
import { FunctionCard } from './components/FunctionCard';
import { CreateEntityDialog } from '../../../components';
import { getAvailableModuleTypesFromInspect } from './moduleTypeOptions';
import type { NetworkFunction } from './types';
import { isFnSaveable } from './validation';
import { API } from '../../../api';
import { usePalette } from '../../../components/command-palette';
import type { Command, RowAdapter } from '../../../components/command-palette';
import './FunctionsPage.scss';

/** Builds a space-joined search string for a function (id, type, chain names, module names/types). */
const fnSearchText = (fn: NetworkFunction): string => {
    const parts: string[] = [fn.id, fn.type];
    for (const chain of fn.chains) {
        parts.push(chain.name);
        for (const m of chain.modules) {
            parts.push(m.name, m.type);
        }
    }
    return parts.join(' ');
};

/**
 * Functions page: tracks editor with horizontal lanes, inline edit, DnD and live counters.
 */
const FunctionsPage = (): React.JSX.Element => {
    const { functions, loading, isDirty, getServerFn, dispatch, saveFn, discardFn, createFn, deleteFn } = useFunctionsData();
    const [availableModuleTypes, setAvailableModuleTypes] = useState<string[]>([]);
    const [availableModuleConfigNamesByType, setAvailableModuleConfigNamesByType] = useState<Record<string, string[]>>({});
    const [availableModuleConfigNames, setAvailableModuleConfigNames] = useState<string[]>([]);
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [flashId, setFlashId] = useState<string | null>(null);
    const [diffOpenId, setDiffOpenId] = useState<string | null>(null);
    const { dragState, startDrag, endDrag } = useDragState();

    useEffect(() => {
        const fetchTypes = async (): Promise<void> => {
            try {
                const resp = await API.inspect.inspect();
                const moduleTypes = getAvailableModuleTypesFromInspect(resp.instance_info?.dp_modules ?? []);
                const cpConfigs = resp.instance_info?.cp_configs ?? [];
                const namesByType = new Map<string, Set<string>>();
                const allNames = new Set<string>();

                cpConfigs.forEach(cfg => {
                    const type = cfg.type?.trim() ?? '';
                    const name = cfg.name?.trim() ?? '';
                    if (!name) {
                        return;
                    }

                    allNames.add(name);
                    if (!type) {
                        return;
                    }

                    const names = namesByType.get(type) ?? new Set<string>();
                    names.add(name);
                    namesByType.set(type, names);
                });

                const byType: Record<string, string[]> = {};
                namesByType.forEach((names, type) => {
                    byType[type] = [...names].sort((a, b) => a.localeCompare(b));
                });

                setAvailableModuleTypes(moduleTypes);
                setAvailableModuleConfigNamesByType(byType);
                setAvailableModuleConfigNames([...allNames].sort((a, b) => a.localeCompare(b)));
            } catch {
                setAvailableModuleTypes([]);
            }
        };
        fetchTypes();
    }, []);

    const anyDirty = useMemo(
        () => functions.some(fn => isDirty(fn.id)),
        [functions, isDirty],
    );

    useUnsavedChangesBlocker(anyDirty);

    const handleSave = useCallback((fnId: string) => (): Promise<void> => saveFn(fnId), [saveFn]);
    const handleDiscard = useCallback((fnId: string) => (): void => discardFn(fnId), [discardFn]);
    const handleDelete = useCallback((fnId: string) => (): Promise<boolean> => deleteFn(fnId), [deleteFn]);

    const jumpToFn = useCallback((id: string): void => {
        setFlashId(null);
        setTimeout(() => {
            setFlashId(id);
            document.getElementById(`fn-card-${id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' });
        }, 0);
    }, []);

    const { setPageContribution } = usePalette();

    const commands = useMemo((): Command[] => {
        const list: Command[] = [];
        if (!loading) {
            list.push({
                id: '__create_function',
                icon: '+',
                label: 'Create function',
                sub: 'Open the create function dialog',
                keywords: 'create new function add',
                onSelect: () => setCreateDialogOpen(true),
            });
        }
        for (const fn of functions) {
            if (isDirty(fn.id) && isFnSaveable(fn)) {
                list.push({
                    id: `__save_${fn.id}`,
                    icon: '✓',
                    label: `Save ${fn.id}`,
                    sub: 'Preview YAML diff before saving',
                    keywords: 'save commit apply',
                    onSelect: () => setDiffOpenId(fn.id),
                });
            }
        }
        return list;
    }, [loading, functions, isDirty]);

    const rowAdapter = useMemo((): RowAdapter<NetworkFunction> => ({
        rows: functions,
        getId: (fn) => fn.id,
        getLabel: (fn) => fn.id,
        getSub: (fn) => `${fn.type} · ${fn.chains.length} chains`,
        searchText: fnSearchText,
        onSelect: (id) => jumpToFn(id),
        icon: '→',
    }), [functions, jumpToFn]);

    useEffect(() => {
        setPageContribution({
            commands,
            rowAdapter: rowAdapter as RowAdapter<unknown>,
            placeholder: 'Search functions or run an action…',
        });
        return () => setPageContribution(null);
    }, [commands, rowAdapter, setPageContribution]);

    const headerContent = (
        <CommandPaletteHeader
            title="Functions"
            placeholder="Search functions or run an action…"
            actions={<Button view="action" onClick={() => setCreateDialogOpen(true)} disabled={loading}>
                <Icon data={Plus} size={16} />
                Create function
            </Button>}
        />
    );

    return (
        <PageLayout header={headerContent}>
            {loading ? (
                <PageLoader loading size="l" />
            ) : (
                <div className="fn-page">
                    {functions.length === 0 ? (
                        <EmptyState message='No functions found. Click "Create function" to add one.' />
                    ) : (
                        functions.map(fn => (
                            <FunctionCard
                                key={fn.id}
                                fn={fn}
                                serverFn={getServerFn(fn.id)}
                                isDirty={isDirty(fn.id)}
                                availableModuleTypes={availableModuleTypes}
                                availableModuleConfigNamesByType={availableModuleConfigNamesByType}
                                availableModuleConfigNames={availableModuleConfigNames}
                                dispatch={dispatch}
                                dragState={dragState}
                                onDragStart={startDrag}
                                onDragEnd={endDrag}
                                onSave={handleSave(fn.id)}
                                onDiscard={handleDiscard(fn.id)}
                                onDelete={handleDelete(fn.id)}
                                diffOpen={diffOpenId === fn.id}
                                onOpenDiff={() => setDiffOpenId(fn.id)}
                                onCloseDiff={() => setDiffOpenId(null)}
                                flash={flashId === fn.id}
                            />
                        ))
                    )}
                </div>
            )}

            <CreateEntityDialog
                entityType="Function"
                open={createDialogOpen}
                onClose={() => setCreateDialogOpen(false)}
                onConfirm={async (name) => {
                    const ok = await createFn(name);
                    if (ok) {
                        setCreateDialogOpen(false);
                    }
                }}
            />
        </PageLayout>
    );
};

export default FunctionsPage;
