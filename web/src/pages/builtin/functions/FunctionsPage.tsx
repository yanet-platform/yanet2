import React, { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { Button, Flex, Icon, Text } from '@gravity-ui/uikit';
import { Plus } from '@gravity-ui/icons';
import { PageLayout, PageLoader, EmptyState, SearchInput } from '../../../components';
import { useFunctionsData } from './hooks/useFunctionsData';
import { useDragState, useUnsavedChangesBlocker } from '../_shared/lane-editor';
import { FunctionCard } from './components/FunctionCard';
import { CreateFunctionDialog } from './dialogs';
import type { NetworkFunction } from './types';
import { API } from '../../../api';
import './FunctionsPage.scss';

/** Returns true if the function matches the query string (case-insensitive substring). */
const matchesFn = (fn: NetworkFunction, query: string): boolean => {
    const q = query.toLowerCase();
    if (fn.id.toLowerCase().includes(q)) {
        return true;
    }
    if (fn.type.toLowerCase().includes(q)) {
        return true;
    }
    for (const chain of fn.chains) {
        if (chain.name.toLowerCase().includes(q)) {
            return true;
        }
        for (const m of chain.modules) {
            if (m.name.toLowerCase().includes(q) || m.type.toLowerCase().includes(q)) {
                return true;
            }
        }
    }
    return false;
};

/**
 * Functions page: Tracks editor with horizontal lanes, inline edit, DnD and live counters.
 */
const FunctionsPage = (): React.JSX.Element => {
    const { functions, loading, isDirty, getServerFn, dispatch, saveFn, discardFn, createFn, deleteFn } = useFunctionsData();
    const [availableModuleTypes, setAvailableModuleTypes] = useState<string[]>([]);
    const [createDialogOpen, setCreateDialogOpen] = useState(false);
    const [searchQuery, setSearchQuery] = useState('');
    const searchRef = useRef<HTMLInputElement>(null);
    const { dragState, startDrag, endDrag } = useDragState();

    useEffect(() => {
        const fetchTypes = async (): Promise<void> => {
            try {
                const resp = await API.inspect.inspect();
                const cpConfigs = resp.instance_info?.cp_configs ?? [];
                const types = [...new Set(cpConfigs.map(c => c.type ?? '').filter(Boolean))];
                setAvailableModuleTypes(types);
            } catch {
                // Non-critical; drawer will show current module type only.
            }
        };
        fetchTypes();
    }, []);

    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent): void => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                searchRef.current?.focus();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, []);

    const filteredFunctions = useMemo(() => {
        if (!searchQuery.trim()) {
            return functions;
        }
        return functions.filter(fn => matchesFn(fn, searchQuery));
    }, [functions, searchQuery]);

    const anyDirty = useMemo(
        () => functions.some(fn => isDirty(fn.id)),
        [functions, isDirty],
    );

    useUnsavedChangesBlocker(anyDirty);

    const handleSave = useCallback((fnId: string) => (): Promise<void> => saveFn(fnId), [saveFn]);
    const handleDiscard = useCallback((fnId: string) => (): void => discardFn(fnId), [discardFn]);
    const handleDelete = useCallback((fnId: string) => (): Promise<boolean> => deleteFn(fnId), [deleteFn]);

    const pageHeader = (
        <Flex alignItems="center" gap={4} style={{ width: '100%' }}>
            <Text variant="header-1">Functions</Text>
            <Flex grow />
            <div style={{ flexBasis: 380, flexShrink: 1 }}>
                <SearchInput
                    controlRef={searchRef}
                    value={searchQuery}
                    onUpdate={setSearchQuery}
                    placeholder="Search functions, chains, modules… (⌘K)"
                />
            </div>
            <Button
                view="action"
                onClick={() => setCreateDialogOpen(true)}
            >
                <Icon data={Plus} size={16} />
                Create function
            </Button>
        </Flex>
    );

    if (loading) {
        return (
            <PageLayout header={pageHeader}>
                <PageLoader loading size="l" />
            </PageLayout>
        );
    }

    return (
        <PageLayout header={pageHeader}>
            <div className="fn-page">
                {filteredFunctions.length === 0 ? (
                    <EmptyState message={
                        searchQuery.trim()
                            ? `No functions match "${searchQuery}".`
                            : 'No functions found. Click "Create function" to add one.'
                    } />
                ) : (
                    filteredFunctions.map(fn => (
                        <FunctionCard
                            key={fn.id}
                            fn={fn}
                            serverFn={getServerFn(fn.id)}
                            isDirty={isDirty(fn.id)}
                            availableModuleTypes={availableModuleTypes}
                            dispatch={dispatch}
                            dragState={dragState}
                            onDragStart={startDrag}
                            onDragEnd={endDrag}
                            onSave={handleSave(fn.id)}
                            onDiscard={handleDiscard(fn.id)}
                            onDelete={handleDelete(fn.id)}
                        />
                    ))
                )}
            </div>

            <CreateFunctionDialog
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
