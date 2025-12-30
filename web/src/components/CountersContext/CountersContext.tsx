import React, { createContext, useContext } from 'react';
import type { InterpolatedCounterData } from '../../hooks';

interface CountersContextValue {
    counters: Map<string, InterpolatedCounterData>;
}

const CountersContext = createContext<CountersContextValue>({
    counters: new Map(),
});

export interface CountersProviderProps {
    counters: Map<string, InterpolatedCounterData>;
    children: React.ReactNode;
}

export const CountersProvider: React.FC<CountersProviderProps> = ({ counters, children }) => {
    return (
        <CountersContext.Provider value={{ counters }}>
            {children}
        </CountersContext.Provider>
    );
};

export const useCounters = (): CountersContextValue => {
    return useContext(CountersContext);
};

export const useNodeCounters = (key: string): InterpolatedCounterData | undefined => {
    const { counters } = useContext(CountersContext);
    return counters.get(key);
};
