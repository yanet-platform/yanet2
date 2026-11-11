// Pure helpers for the ring selector in the pdump config dialog, kept
// apart from the component so the request-building logic is unit-testable
// without rendering the dialog.

import type { RingInfo } from '@yanet/core/api/ring';
import { compareNatural } from '@yanet/core/utils';

/**
 * Smallest ring capacity pdump binds to, in bytes per worker.
 *
 * Mirrors PDUMP_MIN_RING_CAPACITY (modules/pdump/dataplane/record.h): the
 * smallest power-of-two capacity whose largest accepted record holds any
 * pdump record, whatever the snaplen. The service refuses a smaller ring.
 */
export const PDUMP_MIN_RING_CAPACITY = 128 * 1024;

/** Whether pdump accepts the ring: its per-worker capacity is at least the pdump minimum. */
export const ringFitsPdump = (ring: RingInfo): boolean => {
    const capacity = Number(ring.capacity ?? 0);
    return Number.isFinite(capacity) && capacity >= PDUMP_MIN_RING_CAPACITY;
};

export interface RingOption {
    value: string;
    content: string;
    disabled: boolean;
}

/**
 * The ring selector's options, sorted by name.
 *
 * A ring below the pdump minimum is listed but disabled, with the reason in
 * its label, so a ring created elsewhere does not silently vanish. The
 * bound ring stays selectable even if it was since deleted out from under
 * this config, so the edit dialog does not show an empty selector for a
 * config that still has a binding.
 */
export const buildRingOptions = (rings: RingInfo[], isCreate: boolean, boundRingName: string): RingOption[] => {
    const options = rings
        .filter((ring): ring is RingInfo & { name: string } => Boolean(ring.name))
        .map((ring) => {
            const fits = ringFitsPdump(ring);
            return {
                value: ring.name,
                content: fits ? ring.name : `${ring.name} (under 128 KiB, too small for pdump)`,
                disabled: !fits,
            };
        });
    if (!isCreate && boundRingName && !options.some((option) => option.value === boundRingName)) {
        options.push({ value: boundRingName, content: boundRingName, disabled: false });
    }
    return options.sort((a, b) => compareNatural(a.value, b.value));
};

/**
 * Whether the dialog's current ring choice can be submitted.
 *
 * A ring selection is always required, on both create and edit: the service
 * rejects an explicit empty `ring_name`, so clearing the selector on edit
 * must block submission rather than silently resolve to "keep the bound
 * ring" (that meaning is reserved for leaving the selector untouched).
 */
export const isRingSelectionValid = (selectedRing: string): boolean => selectedRing.trim().length > 0;

/**
 * The `ring_name` value to send on `SetConfig`.
 *
 * Create always sends the selected ring. An edit that leaves the bound ring
 * unchanged omits the field so the service keeps the existing binding
 * instead of reading an explicit value; choosing a different ring sends it.
 * The caller only reaches this once the ring selection is valid, so the
 * selected ring is never empty here.
 */
export const resolveRingNameForSubmit = (
    isCreate: boolean,
    selectedRing: string,
    boundRingName: string,
): string | undefined => {
    if (isCreate) return selectedRing;
    if (selectedRing === boundRingName) return undefined;
    return selectedRing;
};
