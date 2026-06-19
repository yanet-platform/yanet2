import React, { createContext, useCallback, useContext, useMemo, useState } from 'react';
import type { Gateway } from './types';
import { loadFromStorage, saveToStorage } from './storage';

export interface GatewayContextValue {
    gateways: Gateway[];
    activeGateway: Gateway | null;
    addGateway: (gw: Omit<Gateway, 'id'>) => void;
    updateGateway: (id: string, updates: Partial<Omit<Gateway, 'id'>>) => void;
    deleteGateway: (id: string) => void;
    setActive: (id: string) => void;
}

export const GatewayContext = createContext<GatewayContextValue>({
    gateways: [],
    activeGateway: null,
    addGateway: () => {},
    updateGateway: () => {},
    deleteGateway: () => {},
    setActive: () => {},
});

/** Provides gateway list and active-selection state to the entire app. */
export const GatewayProvider = ({ children }: { children: React.ReactNode }): React.JSX.Element => {
    const initial = useMemo(() => loadFromStorage(), []);
    const [gateways, setGateways] = useState<Gateway[]>(initial.gateways);
    const [activeId, setActiveId] = useState<string>(initial.activeId);

    const addGateway = useCallback((gw: Omit<Gateway, 'id'>) => {
        const newGateway: Gateway = { ...gw, id: `gw-${Date.now()}` };
        setGateways((prev) => {
            const next = [...prev, newGateway];
            saveToStorage(next, activeId);
            return next;
        });
    }, [activeId]);

    const updateGateway = useCallback((id: string, updates: Partial<Omit<Gateway, 'id'>>) => {
        setGateways((prev) => {
            const target = prev.find((g) => g.id === id);
            if (target?.builtin) {
                return prev;
            }
            const next = prev.map((g) => (g.id === id ? { ...g, ...updates } : g));
            saveToStorage(next, activeId);
            return next;
        });
    }, [activeId]);

    const deleteGateway = useCallback((id: string) => {
        if (gateways.find((g) => g.id === id)?.builtin) {
            return;
        }
        const nextList = gateways.filter((g) => g.id !== id);
        const nextActiveId = activeId !== id
            ? activeId
            : (nextList.find((g) => g.status === 'online') ?? nextList[0])?.id ?? '';
        saveToStorage(nextList, nextActiveId);
        setGateways(nextList);
        setActiveId(nextActiveId);
    }, [gateways, activeId]);

    const setActive = useCallback((id: string) => {
        setActiveId(id);
        saveToStorage(gateways, id);
    }, [gateways]);

    const activeGateway = useMemo(
        () => gateways.find((g) => g.id === activeId) ?? gateways[0] ?? null,
        [gateways, activeId],
    );

    const value = useMemo<GatewayContextValue>(() => ({
        gateways,
        activeGateway,
        addGateway,
        updateGateway,
        deleteGateway,
        setActive,
    }), [gateways, activeGateway, addGateway, updateGateway, deleteGateway, setActive]);

    return (
        <GatewayContext.Provider value={value}>
            {children}
        </GatewayContext.Provider>
    );
};

/** Access the gateway context. Must be used inside GatewayProvider. */
export const useGateways = (): GatewayContextValue => useContext(GatewayContext);
