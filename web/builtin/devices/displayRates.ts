import { deviceTypeManifest } from '@yanet/core/registry';
import type { CounterHistoryEntry } from '@yanet/core/hooks/useCounterHistory';
import type { DeviceCounterData } from '@yanet/core/hooks/useDeviceCounters';

export interface DisplayRates {
    rxPps: number;
    txPps: number;
    rxHistory: number[];
    txHistory: number[];
}

/**
 * Picks the RX/TX pps and sparkline history fields to display for a device.
 *
 * Traffic-generator devices (manifest `generator: true`) never transmit on
 * the wire, so their `outputTx` is legitimately always zero. A generator's
 * frames also leave the input pipeline via the pending-output list when
 * routed elsewhere, which zeroes `inputTx` too — so generators display TX
 * from `inputEntry` instead, which counts every frame the handler emitted
 * into its input pipelines regardless of how it later left; RX stays
 * inputRx (an honest zero). All other devices, including plain/vlan
 * `trafficSource` root interfaces, keep TX from outputTx and are
 * unaffected.
 */
export const displayRates = (
    deviceType: string | undefined,
    counterData: DeviceCounterData | undefined,
    history: CounterHistoryEntry | undefined
): DisplayRates => {
    const isGenerator = !!(deviceType && deviceTypeManifest(deviceType)?.generator);

    return {
        rxPps: counterData?.inputRx.pps ?? 0,
        txPps: (isGenerator ? counterData?.inputEntry.pps : counterData?.outputTx.pps) ?? 0,
        rxHistory: history?.inputRx ?? [],
        txHistory: (isGenerator ? history?.inputEntry : history?.outputTx) ?? [],
    };
};
