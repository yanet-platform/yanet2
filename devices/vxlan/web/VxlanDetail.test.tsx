import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react';
import React from 'react';
import { ApiError } from '@yanet/core/api';
import type { BaseDevice } from '@yanet/core/registry';
import VxlanDetail from './VxlanDetail';

const { updateVxlan, getVxlan } = vi.hoisted(() => ({
    updateVxlan: vi.fn(),
    getVxlan: vi.fn(),
}));

vi.mock('@yanet/core/api', async () => {
    const actual = await vi.importActual<typeof import('@yanet/core/api')>('@yanet/core/api');
    return {
        ...actual,
        devices: { ...actual.devices, updateVxlan, getVxlan },
    };
});

import { deviceType } from './device';

const makeDevice = (ext: Record<string, unknown>, isNew = true, loaded = true): BaseDevice => ({
    id: { type: 'vxlan', name: 'tunnel0' },
    type: 'vxlan',
    inputPipelines: [],
    outputPipelines: [],
    isNew,
    isDirty: isNew,
    loaded,
    ext: { vxlan: ext },
});

// Renders the detail panel against a mutable device whose ext updates the
// same way the Devices page merges patches, and captures the latest state.
// `hydrate` mirrors the merge loadDeviceExt applies when GetDevice resolves.
const renderDetail = (initial: BaseDevice): {
    device: () => BaseDevice;
    hydrate: (ext: Record<string, unknown>) => void;
} => {
    const captured = { device: initial };
    let setDevice: React.Dispatch<React.SetStateAction<BaseDevice>> | undefined;
    const Harness = (): React.JSX.Element => {
        const [device, setState] = React.useState(initial);
        setDevice = setState;
        const handleUpdateExt = (patch: Record<string, unknown>): void => {
            const current = (device.ext.vxlan as Record<string, unknown> | undefined) ?? {};
            const next = { ...device, ext: { ...device.ext, vxlan: { ...current, ...patch } } };
            captured.device = next;
            setState(next);
        };
        return <VxlanDetail device={device} ext={device.ext.vxlan} onUpdateExt={handleUpdateExt} />;
    };
    render(<Harness />);
    return {
        device: () => captured.device,
        hydrate: (ext) => {
            act(() => {
                setDevice?.((prev) => ({ ...prev, ext: { ...prev.ext, vxlan: ext }, loaded: true }));
            });
        },
    };
};

describe('VxlanDetail editors', () => {
    beforeEach(() => {
        updateVxlan.mockReset();
        updateVxlan.mockResolvedValue({});
        getVxlan.mockReset();
    });

    afterEach(() => {
        cleanup();
    });

    it('seeds create defaults into the editors', () => {
        renderDetail(makeDevice(deviceType.createDefaults!()));
        expect(screen.getByLabelText('VNI')).toHaveValue('0');
        expect(screen.getByLabelText('Dst port')).toHaveValue('4789');
        expect(screen.getByLabelText('Src MAC')).toHaveValue('');
        expect(screen.getByLabelText('Dst MAC')).toHaveValue('');
        expect(screen.getByLabelText('Src IP')).toHaveValue('');
        expect(screen.getByLabelText('Dst IP')).toHaveValue('');
    });

    it('loads existing ext values into the editors', () => {
        renderDetail(makeDevice({
            vni: 100,
            dstPort: 4790,
            srcMac: 'aa:bb:cc:dd:ee:ff',
            dstMac: '11:22:33:44:55:66',
            srcIp: '10.0.0.1',
            dstIp: '10.0.0.2',
        }, false));
        expect(screen.getByLabelText('VNI')).toHaveValue('100');
        expect(screen.getByLabelText('Dst port')).toHaveValue('4790');
        expect(screen.getByLabelText('Src MAC')).toHaveValue('aa:bb:cc:dd:ee:ff');
        expect(screen.getByLabelText('Dst MAC')).toHaveValue('11:22:33:44:55:66');
        expect(screen.getByLabelText('Src IP')).toHaveValue('10.0.0.1');
        expect(screen.getByLabelText('Dst IP')).toHaveValue('10.0.0.2');
    });

    it('hydrates an existing device via GetDevice and save resubmits the fetched values', async () => {
        getVxlan.mockResolvedValue({
            name: 'tunnel0',
            vni: 100,
            dst_port: 4790,
            src_mac: 'aa:bb:cc:dd:ee:ff',
            dst_mac: '11:22:33:44:55:66',
            src_ip: '10.0.0.1',
            dst_ip: '10.0.0.2',
        });

        // The same merge the Devices page's loadDeviceExt applies when an
        // existing device is first opened.
        const existing = makeDevice({}, false);
        const loadedExt = await deviceType.loadData!(existing);
        expect(getVxlan).toHaveBeenCalledWith({ name: 'tunnel0' });

        const { device } = renderDetail({ ...existing, ext: { vxlan: loadedExt } });
        expect(screen.getByLabelText('VNI')).toHaveValue('100');
        expect(screen.getByLabelText('Dst port')).toHaveValue('4790');
        expect(screen.getByLabelText('Src MAC')).toHaveValue('aa:bb:cc:dd:ee:ff');
        expect(screen.getByLabelText('Dst MAC')).toHaveValue('11:22:33:44:55:66');
        expect(screen.getByLabelText('Src IP')).toHaveValue('10.0.0.1');
        expect(screen.getByLabelText('Dst IP')).toHaveValue('10.0.0.2');

        // Saving without touching the editors resubmits the fetched values,
        // not the blanks a hydrated device would otherwise send.
        await deviceType.save(device(), makeDevice(loadedExt, false));
        expect(updateVxlan).toHaveBeenCalledWith({
            name: 'tunnel0',
            device: { input: [], output: [] },
            vni: 100,
            dst_port: 4790,
            src_mac: 'aa:bb:cc:dd:ee:ff',
            dst_mac: '11:22:33:44:55:66',
            src_ip: '10.0.0.1',
            dst_ip: '10.0.0.2',
        });
    });

    it('treats a GetDevice 404 as a missing config and hydrates an empty slice', async () => {
        getVxlan.mockRejectedValue(new ApiError(404, 'Not Found', 'no config found'));
        const ext = await deviceType.loadData!(makeDevice({}, false));
        expect(ext).toEqual({});
    });

    it('propagates a non-404 GetDevice failure instead of swallowing it', async () => {
        getVxlan.mockRejectedValue(new ApiError(503, 'Service Unavailable', 'no backend'));
        await expect(deviceType.loadData!(makeDevice({}, false)))
            .rejects.toMatchObject({ status: 503 });

        // A rejection that is not an ApiError at all (e.g. a network-level
        // TypeError) must not be mistaken for a missing config either.
        getVxlan.mockRejectedValue(new TypeError('Failed to fetch'));
        await expect(deviceType.loadData!(makeDevice({}, false))).rejects.toBeInstanceOf(TypeError);
    });

    it('defers the editors until hydration lands, then renders its values', () => {
        const { hydrate } = renderDetail(makeDevice({}, false, false));
        expect(screen.queryByLabelText('VNI')).not.toBeInTheDocument();
        expect(screen.queryByLabelText('Src MAC')).not.toBeInTheDocument();
        expect(screen.getByText('Loading tunnel configuration…')).toBeVisible();

        hydrate({
            vni: 100,
            dstPort: 4790,
            srcMac: 'aa:bb:cc:dd:ee:ff',
            dstMac: '11:22:33:44:55:66',
            srcIp: '10.0.0.1',
            dstIp: '10.0.0.2',
        });
        expect(screen.getByLabelText('VNI')).toHaveValue('100');
        expect(screen.getByLabelText('Dst port')).toHaveValue('4790');
        expect(screen.getByLabelText('Src MAC')).toHaveValue('aa:bb:cc:dd:ee:ff');
        expect(screen.getByLabelText('Dst IP')).toHaveValue('10.0.0.2');
    });

    it('emits the edited values in the UpdateDevice request on save', async () => {
        const { device } = renderDetail(makeDevice(deviceType.createDefaults!()));

        fireEvent.change(screen.getByLabelText('VNI'), { target: { value: '42' } });
        fireEvent.change(screen.getByLabelText('Dst port'), { target: { value: '4790' } });
        fireEvent.change(screen.getByLabelText('Src MAC'), { target: { value: 'aa:bb:cc:dd:ee:ff' } });
        fireEvent.change(screen.getByLabelText('Dst MAC'), { target: { value: '11:22:33:44:55:66' } });
        fireEvent.change(screen.getByLabelText('Src IP'), { target: { value: '10.0.0.1' } });
        fireEvent.change(screen.getByLabelText('Dst IP'), { target: { value: '10.0.0.2' } });

        await deviceType.save(device(), undefined);

        expect(updateVxlan).toHaveBeenCalledTimes(1);
        expect(updateVxlan).toHaveBeenCalledWith({
            name: 'tunnel0',
            device: { input: [], output: [] },
            vni: 42,
            dst_port: 4790,
            src_mac: 'aa:bb:cc:dd:ee:ff',
            dst_mac: '11:22:33:44:55:66',
            src_ip: '10.0.0.1',
            dst_ip: '10.0.0.2',
        });
    });

    it('re-edits a previously saved value and keeps the rest intact', async () => {
        const saved = {
            vni: 100,
            dstPort: 4790,
            srcMac: 'aa:bb:cc:dd:ee:ff',
            dstMac: '11:22:33:44:55:66',
            srcIp: '10.0.0.1',
            dstIp: '10.0.0.2',
        };
        const { device } = renderDetail(makeDevice(saved, false));

        fireEvent.change(screen.getByLabelText('VNI'), { target: { value: '101' } });

        const edited = device();
        expect(deviceType.extDirty?.(edited, makeDevice(saved, false))).toBe(true);
        expect(deviceType.extDirty?.(makeDevice(saved, false), makeDevice(saved, false))).toBe(false);

        await deviceType.save(edited, makeDevice(saved, false));
        expect(updateVxlan).toHaveBeenCalledWith({
            name: 'tunnel0',
            device: { input: [], output: [] },
            vni: 101,
            dst_port: 4790,
            src_mac: 'aa:bb:cc:dd:ee:ff',
            dst_mac: '11:22:33:44:55:66',
            src_ip: '10.0.0.1',
            dst_ip: '10.0.0.2',
        });
    });

    it('flags invalid input and blocks save until every field is valid', async () => {
        const { device } = renderDetail(makeDevice(deviceType.createDefaults!()));

        fireEvent.change(screen.getByLabelText('VNI'), { target: { value: '42' } });
        fireEvent.change(screen.getByLabelText('VNI'), { target: { value: '4x2' } });
        expect(screen.getByText('whole number only')).toBeVisible();
        // The invalid text reaches the ext, so Save is disabled for
        // exactly what the editor shows instead of silently submitting
        // the last valid number.
        expect(deviceType.extValid?.(device())).toBe(false);
        await expect(deviceType.save(device(), undefined)).rejects.toThrow();
        expect(updateVxlan).not.toHaveBeenCalled();

        fireEvent.change(screen.getByLabelText('Dst port'), { target: { value: '0' } });
        expect(screen.getByText('1…65535')).toBeVisible();
        expect(deviceType.extValid?.(device())).toBe(false);

        fireEvent.change(screen.getByLabelText('VNI'), { target: { value: '42' } });
        fireEvent.change(screen.getByLabelText('Dst port'), { target: { value: '4790' } });
        fireEvent.change(screen.getByLabelText('Src IP'), { target: { value: '300.0.0.1' } });
        expect(screen.getByText('IPv4 address only')).toBeVisible();
        // Src MAC, Dst MAC and Dst IP are still empty at this point.
        expect(screen.getAllByText('required')).toHaveLength(3);
        expect(deviceType.extValid?.(device())).toBe(false);
        await expect(deviceType.save(device(), undefined)).rejects.toThrow();

        fireEvent.change(screen.getByLabelText('Src MAC'), { target: { value: 'aa:bb:cc:dd:ee:ff' } });
        fireEvent.change(screen.getByLabelText('Dst MAC'), { target: { value: '11:22:33:44:55:66' } });
        fireEvent.change(screen.getByLabelText('Src IP'), { target: { value: '10.0.0.1' } });
        fireEvent.change(screen.getByLabelText('Dst IP'), { target: { value: '10.0.0.2' } });
        expect(deviceType.extValid?.(device())).toBe(true);

        await deviceType.save(device(), undefined);
        expect(updateVxlan).toHaveBeenCalledTimes(1);
        expect(updateVxlan).toHaveBeenCalledWith(
            expect.objectContaining({ vni: 42, dst_port: 4790, src_ip: '10.0.0.1' }),
        );
    });

    it('rejects mixed-separator and hyphen-only MACs while colon-only stays valid', () => {
        renderDetail(makeDevice(deviceType.createDefaults!()));
        const srcMac = screen.getByLabelText('Src MAC');

        fireEvent.change(srcMac, { target: { value: 'aa:bb:cc:dd:ee-ff' } });
        expect(srcMac).toHaveClass('dv-vxlan-input--invalid');

        fireEvent.change(srcMac, { target: { value: 'aa-bb-cc-dd-ee-ff' } });
        expect(srcMac).toHaveClass('dv-vxlan-input--invalid');

        fireEvent.change(srcMac, { target: { value: 'aa:bb:cc:dd:ee:ff' } });
        expect(srcMac).not.toHaveClass('dv-vxlan-input--invalid');
    });
});
