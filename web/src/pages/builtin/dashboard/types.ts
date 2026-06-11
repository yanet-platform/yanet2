/** Shared structural and live-snapshot types for the dashboard scene and inspector. */

export interface SelectedItem {
    kind: 'device' | 'pipeline' | 'fn';
    id: string;
}

/** Structural device data derived from instance topology only. */
export interface StructuralDevice {
    id: string;
    name: string;
    kind: 'plain' | 'vlan';
    vlan?: number;
    parent?: string;
    mtu?: number;
    speed?: string;
    pipeIn?: string;
    pipeOut?: string;
}

/** Structural pipeline data derived from instance topology only. */
export interface StructuralPipeline {
    id: string;
    name: string;
    fns: string[];
}

/** Structural function data derived from instance topology only. */
export interface StructuralFunction {
    id: string;
    mod: string;
    chains: number;
}

/** Live per-frame snapshot of rates, trends, and statuses. */
export interface LiveSnapshot {
    devicesById: Map<string, {
        rxPps: number;
        rxBps: number;
        txPps: number;
        txBps: number;
        status: 'ok' | 'idle';
        trendRx: number[];
        trendTx: number[];
    }>;
    pipelinesById: Map<string, {
        pps: number;
        trend: number[];
        status: 'ok' | 'idle';
    }>;
    functionsById: Map<string, {
        pps: number;
        trend: number[];
        status: 'ok' | 'idle';
    }>;
}
