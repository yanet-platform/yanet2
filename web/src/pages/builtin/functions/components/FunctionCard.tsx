import React, { useState, useCallback, useMemo } from 'react';
import type { NetworkFunction, FunctionsAction, DragPayload, Module, Chain } from '../types';
import { useModuleCounters, type ModuleInfo } from '../hooks/useModuleCounters';
import { getDragPayload, useSparklineHistory } from '../../_shared/lane-editor';
import type { DragState } from '../../_shared/lane-editor';
import { FunctionCardHeader } from './FunctionCardHeader';
import { Lane } from './Lane';
import { AddChainButton } from './AddChainButton';
import { Drawer } from './Drawer';
import { DiffModal } from './DiffModal';
import type { InterpolatedCounterData } from '../../../../hooks';

interface FunctionCardProps {
    fn: NetworkFunction;
    serverFn: NetworkFunction | null;
    isDirty: boolean;
    availableModuleTypes: string[];
    availableModuleConfigNamesByType: Record<string, string[]>;
    availableModuleConfigNames: string[];
    dispatch: (action: FunctionsAction) => void;
    dragState: DragState;
    onDragStart: (payload: DragPayload) => void;
    onDragEnd: () => void;
    onSave: () => Promise<void>;
    onDiscard: () => void;
    onDelete: () => Promise<boolean>;
}

/** Validate a single function, returning error messages (editing-time only; weight=0 is allowed). */
const validateFn = (fn: NetworkFunction): string[] => {
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
const validateSave = (fn: NetworkFunction): string[] => {
    const errors: string[] = [];
    const totalWeight = fn.chains.reduce((s, c) => s + c.weight, 0);
    if (totalWeight === 0 && fn.chains.length > 0) {
        errors.push('Total chain weight is 0 — at least one chain must have weight > 0 before saving.');
    }
    return errors;
};

/**
 * A full function card with collapsible lanes, drag-and-drop, drawer, and per-function save.
 */
export const FunctionCard: React.FC<FunctionCardProps> = ({
    fn,
    serverFn,
    isDirty,
    availableModuleTypes,
    availableModuleConfigNamesByType,
    availableModuleConfigNames,
    dispatch,
    dragState,
    onDragStart,
    onDragEnd,
    onSave,
    onDiscard,
    onDelete,
}) => {
    const [collapsed, setCollapsed] = useState(false);
    const [diffOpen, setDiffOpen] = useState(false);
    const [drawerSelection, setDrawerSelection] = useState<
        { kind: 'module'; moduleId: string; chainId: string } |
        { kind: 'chain'; chainId: string } |
        null
    >(null);

    const totalWeight = fn.chains.reduce((s, c) => s + c.weight, 0);

    const errors = useMemo(() => validateFn(fn), [fn]);
    const hasErrors = errors.length > 0;

    const moduleInfoList: ModuleInfo[] = useMemo(() => {
        const list: ModuleInfo[] = [];
        for (const chain of fn.chains) {
            for (const m of chain.modules) {
                list.push({
                    nodeId: m.id,
                    chainName: chain.name,
                    moduleType: m.type,
                    moduleName: m.name,
                });
            }
        }
        return list;
    }, [fn]);

    const { counters } = useModuleCounters(fn.id, moduleInfoList);

    const counterMap: Map<string, InterpolatedCounterData> = useMemo(() => {
        const map = new Map<string, InterpolatedCounterData>();
        for (const [key, val] of counters.entries()) {
            map.set(key, val);
        }
        return map;
    }, [counters]);

    const totalPps = useMemo(() => {
        let sum = 0;
        for (const chain of fn.chains) {
            if (chain.modules.length === 0) {
                continue;
            }
            const firstModule = chain.modules[0];
            sum += counterMap.get(firstModule.id)?.pps ?? 0;
        }
        return sum;
    }, [counterMap, fn.chains]);

    const sparklineData = useSparklineHistory(`fn:${fn.id}:total`, totalPps);

    const siblingChainNames = fn.chains.map(c => c.name);

    const getConfigNameSuggestions = useCallback((moduleType: string): string[] => {
        return (availableModuleConfigNamesByType[moduleType]?.length ?? 0) > 0
            ? availableModuleConfigNamesByType[moduleType]
            : availableModuleConfigNames;
    }, [availableModuleConfigNames, availableModuleConfigNamesByType]);

    const handleDrop = useCallback((toChainId: string, toIdx: number): void => {
        const payload = getDragPayload();
        if (!payload) {
            return;
        }
        dispatch({
            type: 'MOVE_MODULE',
            fromFnId: payload.fromFnId,
            toFnId: fn.id,
            fromChainId: payload.fromChainId,
            toChainId,
            moduleId: payload.moduleId,
            toIdx,
        });
        onDragEnd();
    }, [fn.id, dispatch, onDragEnd]);

    const handleOpenModuleDrawer = useCallback((moduleId: string, chainId: string): void => {
        setDrawerSelection({ kind: 'module', moduleId, chainId });
    }, []);

    const handleOpenChainDrawer = useCallback((chainId: string): void => {
        setDrawerSelection({ kind: 'chain', chainId });
    }, []);

    const handleAddModule = useCallback((chainId: string): void => {
        const chain = fn.chains.find(c => c.id === chainId);
        if (!chain) {
            return;
        }

        const existingNames = new Set(chain.modules.map(module => module.name));
        let idx = chain.modules.length;
        let candidateName = `module${idx}`;
        while (existingNames.has(candidateName)) {
            idx++;
            candidateName = `module${idx}`;
        }

        const module: Module = {
            id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
            type: 'route',
            name: candidateName,
        };

        dispatch({
            type: 'ADD_MODULE',
            fnId: fn.id,
            chainId,
            toIdx: chain.modules.length,
            module,
        });
        setDrawerSelection({ kind: 'module', moduleId: module.id, chainId });
    }, [fn.id, fn.chains, dispatch]);

    const handleCloseDrawer = useCallback((): void => {
        setDrawerSelection(null);
    }, []);

    const handleAddChain = useCallback((): void => {
        const existingNames = new Set(fn.chains.map(c => c.name));
        let idx = fn.chains.length;
        let name = `chain${idx}`;
        while (existingNames.has(name)) {
            idx++;
            name = `chain${idx}`;
        }
        const newChain: Chain = {
            id: `${Date.now()}-${Math.random().toString(36).slice(2)}`,
            name,
            weight: 1,
            modules: [],
        };
        dispatch({ type: 'ADD_CHAIN', fnId: fn.id, chain: newChain });
    }, [fn.id, fn.chains, dispatch]);

    const drawerModule: Module | null = useMemo(() => {
        if (drawerSelection?.kind !== 'module') {
            return null;
        }
        for (const chain of fn.chains) {
            const m = chain.modules.find(mod => mod.id === drawerSelection.moduleId);
            if (m) {
                return m;
            }
        }
        return null;
    }, [fn.chains, drawerSelection]);

    const drawerChain: Chain | null = useMemo(() => {
        const chainId = drawerSelection?.kind === 'module'
            ? drawerSelection.chainId
            : drawerSelection?.kind === 'chain'
                ? drawerSelection.chainId
                : null;
        if (!chainId) {
            return null;
        }
        return fn.chains.find(c => c.id === chainId) ?? null;
    }, [fn.chains, drawerSelection]);

    const saveErrors = useMemo(() => validateSave(fn), [fn]);

    const handleOpenDiff = useCallback((): void => {
        setDiffOpen(true);
    }, []);

    const handleCloseDiff = useCallback((): void => {
        setDiffOpen(false);
    }, []);

    return (
        <div className={`fn-function-card${collapsed ? ' fn-function-card--collapsed' : ''}`}>
            <FunctionCardHeader
                fn={fn}
                isDirty={isDirty}
                collapsed={collapsed}
                hasErrors={hasErrors}
                totalPps={totalPps}
                sparklineData={sparklineData}
                onToggleCollapse={() => setCollapsed(c => !c)}
                onOpenDiff={handleOpenDiff}
                onDiscard={onDiscard}
                onDelete={onDelete}
            />

            {!collapsed && (
                <div className="fn-function-card__body">
                    {hasErrors && (
                        <div className="fn-function-card__errors">
                            {errors.map((e, idx) => (
                                <div key={idx} className="fn-function-card__error-item">{e}</div>
                            ))}
                        </div>
                    )}

                    <div className="fn-function-card__sub-header">
                        <span className="fn-function-card__sub-header-item fn-function-card__sub-header-item--bold">
                            {fn.chains.length} chains
                        </span>
                        <span className="fn-function-card__sub-header-sep" />
                        <span className="fn-function-card__sub-header-item">
                            {fn.chains.reduce((s, c) => s + c.modules.length, 0)} modules
                        </span>
                        <span className="fn-function-card__sub-header-sep" />
                        <span className="fn-function-card__sub-header-item">
                            Σ weight {totalWeight}
                        </span>
                        <div style={{ flex: 1 }} />
                        <AddChainButton onClick={handleAddChain} />
                    </div>

                    <div className="fn-function-card__lanes">
                        {fn.chains.map(chain => (
                            <Lane
                                key={chain.id}
                                fnId={fn.id}
                                chain={chain}
                                totalWeight={totalWeight}
                                dragState={dragState}
                                counterMap={counterMap}
                                siblingChainNames={siblingChainNames}
                                onDragStart={onDragStart}
                                onDragEnd={onDragEnd}
                                onDrop={handleDrop}
                                dispatch={dispatch}
                                onAddModule={handleAddModule}
                                onOpenModuleDrawer={handleOpenModuleDrawer}
                                onOpenChainDrawer={handleOpenChainDrawer}
                            />
                        ))}
                    </div>
                </div>
            )}

            {drawerSelection?.kind === 'module' && drawerModule && (
                <Drawer
                    mode="module"
                    module={drawerModule}
                    chain={drawerChain}
                    counter={counterMap.get(drawerSelection.moduleId)}
                    availableTypes={availableModuleTypes}
                    availableModuleConfigNames={getConfigNameSuggestions(drawerModule.type)}
                    onClose={handleCloseDrawer}
                    onRename={name => {
                        dispatch({ type: 'RENAME_MODULE', fnId: fn.id, moduleId: drawerModule.id, name });
                    }}
                    onChangeType={newType => {
                        dispatch({
                            type: 'UPDATE_MODULE_CONFIG',
                            fnId: fn.id,
                            moduleId: drawerModule.id,
                            patch: { type: newType },
                        });
                    }}
                    onRemove={() => {
                        dispatch({
                            type: 'REMOVE_MODULE',
                            fnId: fn.id,
                            chainId: drawerSelection.chainId,
                            moduleId: drawerModule.id,
                        });
                        handleCloseDrawer();
                    }}
                />
            )}
            {drawerSelection?.kind === 'chain' && drawerChain && (
                <Drawer
                    mode="chain"
                    chain={drawerChain}
                    fnId={fn.id}
                    onClose={handleCloseDrawer}
                    onUpdateChain={patch => {
                        dispatch({ type: 'UPDATE_CHAIN', fnId: fn.id, chainId: drawerChain.id, patch });
                    }}
                    onRemoveChain={() => {
                        dispatch({ type: 'REMOVE_CHAIN', fnId: fn.id, chainId: drawerChain.id });
                        handleCloseDrawer();
                    }}
                    onReorderModule={(fromIdx, toIdx) => {
                        const m = drawerChain.modules[fromIdx];
                        if (m) {
                            dispatch({
                                type: 'MOVE_MODULE',
                                fromFnId: fn.id,
                                toFnId: fn.id,
                                fromChainId: drawerChain.id,
                                toChainId: drawerChain.id,
                                moduleId: m.id,
                                toIdx,
                            });
                        }
                    }}
                    onAddModuleToChain={(module) => {
                        dispatch({
                            type: 'ADD_MODULE',
                            fnId: fn.id,
                            chainId: drawerChain.id,
                            toIdx: drawerChain.modules.length,
                            module,
                        });
                        setDrawerSelection({ kind: 'module', moduleId: module.id, chainId: drawerChain.id });
                    }}
                />
            )}

            {diffOpen && (
                <DiffModal
                    fn={fn}
                    serverFn={serverFn}
                    saveErrors={saveErrors}
                    onClose={handleCloseDiff}
                    onApply={onSave}
                />
            )}
        </div>
    );
};
