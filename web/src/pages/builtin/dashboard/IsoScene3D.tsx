import React, { useEffect, useRef, useState, useMemo } from 'react';
import * as THREE from 'three';
import type { InstanceInfo } from '../../../api/inspect';
import type { DeviceCounterData, DeviceAbsoluteData } from '../../../hooks';
import { usePipelineCounters, useFunctionCounters, useDeviceTrendSeries } from '../inspect/hooks';
import { fmtPps } from '../inspect/formatters';
import { Inspector } from './Inspector';
import type { SelectedItem } from './Inspector';
import type { AgentUsage } from '../inspect/utils';
import { metaFor } from '../functions/moduleMeta';

const PARTICLE_MIN_REF_PPS = 10;
const PARTICLE_REF_DECAY = 0.995;
const PARTICLE_MAX_DT = 0.05;
const PARTICLE_MIN_VISIBLE_FRACTION = 0.15;

export interface IsoScene3DProps {
    instance: InstanceInfo;
    rateCounters: Map<string, DeviceCounterData>;
    absoluteCounters: Map<string, DeviceAbsoluteData>;
    usage: Map<string, AgentUsage>;
}

interface LabelInfo {
    kind: 'device' | 'pipeline' | 'fn';
    id: string;
    anchor: 'right' | 'pipeline' | 'top';
    worldPos: () => THREE.Vector3;
    text: string;
    subtext: string;
    active?: boolean;
    sx: number;
    sy: number;
}

interface FwdPath {
    pts: THREE.Vector3[];
    lens: number[];
    total: number;
    density: number;
    deviceId: string;
}

interface Particle {
    pi: number;
    t: number;
    speed: number;
    posIdx: number;
    lastFraction: number;
}

interface ParticleSystem {
    points: THREE.Points | null;
    ps: Particle[];
    geo: THREE.BufferGeometry | null;
}

interface FnMeshEntry {
    mesh: THREE.Mesh;
    fnId: string;
    baseH: number;
    x: number;
    z: number;
    w: number;
    d: number;
    activeFill: number;
    activeEdge: number;
    activeEmissive: number;
}

interface WireEntry {
    line: THREE.Line;
    deviceId: string;
    pipeId: string;
}

interface ThreeRefs {
    scene: THREE.Scene | null;
    camera: THREE.OrthographicCamera | null;
    renderer: THREE.WebGLRenderer | null;
    pickGroup: THREE.Group | null;
    deviceMeshes: Record<string, THREE.Mesh>;
    pipeMeshes: Record<string, THREE.Mesh>;
    fnMeshes: FnMeshEntry[];
    fwdPS: ParticleSystem;
    fwdPaths: FwdPath[];
    labelSources: Omit<LabelInfo, 'sx' | 'sy'>[];
    wires: WireEntry[];
    sceneCenter: THREE.Vector3 | null;
}

/** Structural device data derived only from instance topology (no live counters). */
interface StructuralDevice {
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

/** Structural pipeline data derived only from instance topology. */
interface StructuralPipeline {
    id: string;
    name: string;
    fns: string[];
}

/** Structural function data derived only from instance topology. */
interface StructuralFunction {
    id: string;
    mod: string;
    chains: number;
}

/** Per-frame live snapshot: rates, trends, statuses. Updated every counter tick. */
interface LiveSnapshot {
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

// Reference pps denominator and maximum height multiplier for fn cubes.
const FN_REF_PPS = 1e6;
const FN_MAX_GROWTH = 7;

/** Build a box mesh anchored at its bottom face. */
const makeBox = (
    w: number,
    h: number,
    d: number,
    fillHex: number,
    opacity: number = 1,
    emissive: number = 0x000000,
): THREE.Mesh => {
    const geo = new THREE.BoxGeometry(w, h, d);
    geo.translate(0, h / 2, 0);
    const mat = new THREE.MeshLambertMaterial({
        color: fillHex,
        transparent: opacity < 1,
        opacity,
        emissive,
        emissiveIntensity: emissive ? 0.4 : 0,
    });
    const mesh = new THREE.Mesh(geo, mat);
    const edges = new THREE.LineSegments(
        new THREE.EdgesGeometry(geo),
        new THREE.LineBasicMaterial({ color: 0x3a3731, transparent: true, opacity: 0.85 }),
    );
    mesh.add(edges);
    mesh.userData.edges = edges;
    return mesh;
};

/** Build a particle system from an array of paths. */
const buildParticles = (
    paths: FwdPath[],
    color: number,
    sizeBase: number,
): ParticleSystem => {
    const N = paths.reduce((sum, p) => sum + Math.max(1, Math.round(p.density * 18)), 0);
    if (!N) return { points: null, ps: [], geo: null };

    const positions = new Float32Array(N * 3);
    const ps: Particle[] = [];
    let idx = 0;
    paths.forEach((path, pi) => {
        const count = Math.max(1, Math.round(path.density * 18));
        for (let i = 0; i < count; i++) {
            ps.push({
                pi,
                t: Math.random(),
                speed: 0.0008 + path.density * 0.0014,
                posIdx: idx,
                lastFraction: 0,
            });
            positions[idx * 3 + 0] = 0;
            positions[idx * 3 + 1] = 0;
            positions[idx * 3 + 2] = 0;
            idx++;
        }
    });

    const geo = new THREE.BufferGeometry();
    geo.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    const mat = new THREE.PointsMaterial({
        color,
        size: sizeBase,
        transparent: true,
        opacity: 0.7,
        sizeAttenuation: false,
        depthWrite: false,
    });
    const points = new THREE.Points(geo, mat);
    return { points, ps, geo };
};

/** Isometric 3D topology scene: devices, pipelines, functions, and forward particles. */
export const IsoScene3D: React.FC<IsoScene3DProps> = ({
    instance,
    rateCounters,
    absoluteCounters,
    usage,
}) => {
    const devices = instance.devices ?? [];
    const pipelines = instance.pipelines ?? [];
    const functions = instance.functions ?? [];

    const structuralDevices = useMemo((): StructuralDevice[] =>
        devices.map((d, idx) => ({
            id: d.name ?? `device-${idx}`,
            name: d.name ?? `device-${idx}`,
            kind: d.type === 'vlan' ? 'vlan' : 'plain',
            pipeIn: d.input_pipelines?.[0]?.name,
            pipeOut: d.output_pipelines?.[0]?.name,
        })),
    [devices]);

    const structuralPipelines = useMemo((): StructuralPipeline[] =>
        pipelines.map((p, idx) => ({
            id: p.name ?? `pipeline-${idx}`,
            name: p.name ?? `pipeline-${idx}`,
            fns: p.functions ?? [],
        })),
    [pipelines]);

    const structuralFunctions = useMemo((): StructuralFunction[] =>
        functions.map((f, idx) => ({
            id: f.name ?? `function-${idx}`,
            mod: f.chains?.[0]?.modules?.[0]?.type ?? '',
            chains: f.chains?.length ?? 0,
        })),
    [functions]);

    const deviceNames = useMemo(
        () => structuralDevices.map((d) => d.name),
        [structuralDevices],
    );

    const pipelineNames = useMemo(
        () => structuralPipelines.map((p) => p.name),
        [structuralPipelines],
    );

    const functionNames = useMemo(
        () => structuralFunctions.map((f) => f.id),
        [structuralFunctions],
    );

    const { rates: pipeRates, series: pipeSeries } = usePipelineCounters(
        deviceNames,
        pipelineNames,
        deviceNames.length > 0 && pipelineNames.length > 0,
    );

    const { rates: fnRates, series: fnSeries } = useFunctionCounters(
        deviceNames,
        pipelineNames,
        functionNames,
        deviceNames.length > 0 && pipelineNames.length > 0 && functionNames.length > 0,
    );

    const trendRxMap = useDeviceTrendSeries(rateCounters, 'rx');
    const trendTxMap = useDeviceTrendSeries(rateCounters, 'tx');

    const liveRef = useRef<LiveSnapshot>({
        devicesById: new Map(),
        pipelinesById: new Map(),
        functionsById: new Map(),
    });
    liveRef.current = useMemo<LiveSnapshot>(() => {
        const devicesById = new Map<string, {
            rxPps: number;
            rxBps: number;
            txPps: number;
            txBps: number;
            status: 'ok' | 'idle';
            trendRx: number[];
            trendTx: number[];
        }>();
        structuralDevices.forEach((d) => {
            const counters = rateCounters.get(d.id);
            const abs = absoluteCounters.get(d.id);
            const rxPps = counters?.rx?.pps ?? 0;
            const rxBps = counters?.rx?.bps ?? 0;
            const txPps = counters?.tx?.pps ?? 0;
            const txBps = counters?.tx?.bps ?? 0;
            const status: 'ok' | 'idle' =
                abs && (abs.rx.packets > 0 || abs.tx.packets > 0) ? 'ok' : 'idle';
            devicesById.set(d.id, {
                rxPps,
                rxBps,
                txPps,
                txBps,
                status,
                trendRx: trendRxMap.get(d.id) ?? [],
                trendTx: trendTxMap.get(d.id) ?? [],
            });
        });
        const pipelinesById = new Map<string, {
            pps: number;
            trend: number[];
            status: 'ok' | 'idle';
        }>();
        structuralPipelines.forEach((p) => {
            const rate = pipeRates.get(p.id);
            const pps = rate?.pps ?? 0;
            pipelinesById.set(p.id, {
                pps,
                trend: pipeSeries.get(p.id) ?? [],
                status: pps > 0 ? 'ok' : 'idle',
            });
        });
        const functionsById = new Map<string, {
            pps: number;
            trend: number[];
            status: 'ok' | 'idle';
        }>();
        structuralFunctions.forEach((f) => {
            const rate = fnRates.get(f.id);
            const pps = rate?.pps ?? 0;
            functionsById.set(f.id, {
                pps,
                trend: fnSeries.get(f.id) ?? [],
                status: pps > 0 ? 'ok' : 'idle',
            });
        });
        return { devicesById, pipelinesById, functionsById };
    }, [
        structuralDevices, structuralPipelines, structuralFunctions,
        rateCounters, absoluteCounters,
        pipeRates, pipeSeries,
        fnRates, fnSeries,
        trendRxMap, trendTxMap,
    ]);

    const containerRef = useRef<HTMLDivElement>(null);
    const canvasMountRef = useRef<HTMLDivElement>(null);
    const [selected, setSelected] = useState<SelectedItem | null>(null);
    const [labels, setLabels] = useState<LabelInfo[]>([]);
    const [canvasSize, setCanvasSize] = useState({ w: 900, h: 620 });
    const [webglAvailable, setWebglAvailable] = useState<boolean | null>(null);
    const [rendererError, setRendererError] = useState(false);
    const threeRef = useRef<ThreeRefs>({
        scene: null,
        camera: null,
        renderer: null,
        pickGroup: null,
        deviceMeshes: {},
        pipeMeshes: {},
        fnMeshes: [],
        fwdPS: { points: null, ps: [], geo: null },
        fwdPaths: [],
        labelSources: [],
        wires: [],
        sceneCenter: null,
    });
    const canvasSizeRef = useRef(canvasSize);
    canvasSizeRef.current = canvasSize;
    const peakPpsRef = useRef<number>(PARTICLE_MIN_REF_PPS);

    useEffect(() => {
        const check = (): boolean => {
            try {
                const canvas = document.createElement('canvas');
                const gl =
                    canvas.getContext('webgl2') ||
                    canvas.getContext('webgl') ||
                    canvas.getContext('experimental-webgl');
                return !!gl;
            } catch {
                return false;
            }
        };
        setWebglAvailable(check());
    }, []);

    useEffect(() => {
        if (!containerRef.current) return;
        const obs = new ResizeObserver((entries) => {
            const entry = entries[0];
            if (!entry) return;
            const { width } = entry.contentRect;
            setCanvasSize({ w: Math.max(300, Math.floor(width)), h: 620 });
        });
        obs.observe(containerRef.current);
        const { width } = containerRef.current.getBoundingClientRect();
        if (width > 0) {
            setCanvasSize({ w: Math.floor(width), h: 620 });
        }
        return () => obs.disconnect();
    }, []);

    useEffect(() => {
        const r = threeRef.current;
        if (!r.renderer || !r.camera) return;
        const { w, h } = canvasSize;
        r.renderer.setSize(w, h);
        const aspect = w / h;
        const camSize = 360;
        const cam = r.camera;
        cam.left = -camSize * aspect / 2;
        cam.right = camSize * aspect / 2;
        cam.top = camSize / 2;
        cam.bottom = -camSize / 2;
        cam.updateProjectionMatrix();
    }, [canvasSize]);

    useEffect(() => {
        if (webglAvailable !== true) {
            return;
        }
        if (!canvasMountRef.current) return;
        let cancelled = false;
        const mount = canvasMountRef.current;
        while (mount.firstChild) mount.removeChild(mount.firstChild);

        const { w: VW, h: VH } = canvasSize;
        const scene = new THREE.Scene();
        scene.background = null;

        const aspect = VW / VH;
        const camSize = 360;
        const camera = new THREE.OrthographicCamera(
            -camSize * aspect / 2,
            camSize * aspect / 2,
            camSize / 2,
            -camSize / 2,
            -4000,
            4000,
        );

        let renderer: THREE.WebGLRenderer;
        // Three.js calls console.error multiple times during failed WebGL
        // initialisation before throwing; silence them so a graceful fallback
        // does not look like a stream of crashes in the console. Restored on
        // both branches immediately after construction returns or throws.
        const origError = console.error;
        const origWarn = console.warn;
        console.error = (): void => {};
        console.warn = (): void => {};
        try {
            renderer = new THREE.WebGLRenderer({ alpha: true, antialias: true });
            console.error = origError;
            console.warn = origWarn;
        } catch (err) {
            console.error = origError;
            console.warn = origWarn;
            if (!cancelled) {
                setRendererError(true);
            }
            return;
        }
        renderer.setPixelRatio(window.devicePixelRatio || 1);
        renderer.setSize(VW, VH);
        renderer.setClearColor(0x000000, 0);
        mount.appendChild(renderer.domElement);
        renderer.domElement.style.display = 'block';

        scene.add(new THREE.AmbientLight(0xfff3e0, 0.55));
        const dir = new THREE.DirectionalLight(0xffe6cc, 0.5);
        dir.position.set(200, 600, 300);
        scene.add(dir);
        scene.add(new THREE.HemisphereLight(0xc9a070, 0x0d0d0c, 0.35));

        const numPipes = structuralPipelines.length;
        const LANE_LEN = 480;
        const LANE_SPACING = Math.max(28, Math.min(46, 230 / Math.max(1, numPipes - 1)));
        const LANE_HALF_W = Math.min(17, LANE_SPACING * 0.45);
        const LANE_THICK = 4;
        const X0 = 0;
        const Z0 = 0;

        const plainDevs = structuralDevices.filter((d) => d.kind === 'plain');
        const vlanDevs = structuralDevices.filter((d) => d.kind === 'vlan');
        const devW = 26;
        const devD = 14;
        const devH = 7;
        const colSpan = (numPipes - 1) * LANE_SPACING + LANE_HALF_W * 2;
        const devGap = Math.max(1, Math.min(4, (colSpan - plainDevs.length * devD) / Math.max(1, plainDevs.length - 1)));
        const vlanGap = Math.max(1, Math.min(4, (colSpan - vlanDevs.length * devD) / Math.max(1, vlanDevs.length - 1)));

        const devicePositions: Record<string, { x: number; z: number; kind: 'plain' | 'vlan' }> = {};
        plainDevs.forEach((d, i) => {
            devicePositions[d.id] = { x: -140, z: Z0 + i * (devD + devGap), kind: 'plain' };
        });
        vlanDevs.forEach((d, i) => {
            devicePositions[d.id] = { x: -90, z: Z0 + i * (devD + vlanGap), kind: 'vlan' };
        });

        const pipeZ = structuralPipelines.map((_, i) => Z0 + i * LANE_SPACING);

        const pickGroup = new THREE.Group();
        scene.add(pickGroup);

        const deviceMeshes: Record<string, THREE.Mesh> = {};
        structuralDevices.forEach((d) => {
            const pos = devicePositions[d.id];
            if (!pos) return;
            const isVlan = d.kind === 'vlan';
            const color = 0x1d1b18;
            const edgeColor = 0x3a3731;
            const mesh = makeBox(devW, devH, devD, color, 0.95);
            ((mesh.userData.edges as THREE.LineSegments).material as THREE.LineBasicMaterial).color.setHex(edgeColor);
            mesh.position.set(pos.x + devW / 2, 0, pos.z + devD / 2);
            mesh.userData = {
                ...mesh.userData,
                kind: 'device',
                id: d.id,
                baseColor: color,
                edgeColor,
                isVlan,
            };
            pickGroup.add(mesh);
            deviceMeshes[d.id] = mesh;
        });

        const pipeMeshes: Record<string, THREE.Mesh> = {};
        structuralPipelines.forEach((p, pi) => {
            const z = pipeZ[pi];
            const color = 0x1a1816;
            const edgeColor = 0x3a3731;
            const mesh = makeBox(LANE_LEN, LANE_THICK, LANE_HALF_W * 2, color, 0.9);
            const edgesMat = (mesh.userData.edges as THREE.LineSegments).material as THREE.LineBasicMaterial;
            edgesMat.color.setHex(edgeColor);
            edgesMat.opacity = 0.5;
            mesh.position.set(X0 + LANE_LEN / 2, 0, z);
            mesh.userData = { ...mesh.userData, kind: 'pipeline', id: p.id, baseColor: color, edgeColor };
            pickGroup.add(mesh);
            pipeMeshes[p.id] = mesh;
        });

        const FN_DEPTH = Math.max(10, LANE_HALF_W * 1.6);
        const fnMeshes: FnMeshEntry[] = [];
        structuralPipelines.forEach((p, pi) => {
            if (!p.fns.length) return;
            const slotLen = (LANE_LEN - 40) / p.fns.length;
            p.fns.forEach((fname, fi) => {
                const xStart = X0 + 22 + slotLen * fi + 3;
                const w = Math.max(18, slotLen - 9);
                const baseH = 5;
                const color = 0x1a1816;
                const edgeColor = 0x3a3731;
                const mesh = makeBox(w, baseH, FN_DEPTH, color, 0.92, 0x000000);
                ((mesh.userData.edges as THREE.LineSegments).material as THREE.LineBasicMaterial).color.setHex(edgeColor);
                mesh.position.set(xStart + w / 2, LANE_THICK, pipeZ[pi]);
                mesh.userData = {
                    ...mesh.userData,
                    kind: 'fn',
                    id: fname,
                    baseColor: color,
                    edgeColor,
                };
                pickGroup.add(mesh);
                const fn = structuralFunctions.find((sf) => sf.id === fname);
                const tint = new THREE.Color(metaFor(fn?.mod ?? '').color);
                const activeEdge = tint.getHex();
                const activeFill = tint.clone().multiplyScalar(0.22).getHex();
                const activeEmissive = tint.clone().multiplyScalar(0.20).getHex();
                fnMeshes.push({
                    mesh,
                    fnId: fname,
                    baseH,
                    x: xStart,
                    z: pipeZ[pi] - FN_DEPTH / 2,
                    w,
                    d: FN_DEPTH,
                    activeFill,
                    activeEdge,
                    activeEmissive,
                });
            });
        });

        {
            const gridSize = 700;
            const gridDiv = 18;
            const grid = new THREE.GridHelper(gridSize, gridDiv, 0x2a2823, 0x1f1d1a);
            (grid.material as THREE.Material).opacity = 0.45;
            (grid.material as THREE.Material).transparent = true;
            grid.position.set(120, -0.5, colSpan / 2);
            scene.add(grid);
        }

        const wires: WireEntry[] = [];
        structuralDevices.forEach((d) => {
            if (!d.pipeIn) return;
            const pi = structuralPipelines.findIndex((p) => p.id === d.pipeIn);
            if (pi < 0) return;
            const pos = devicePositions[d.id];
            if (!pos) return;
            const pts = [
                new THREE.Vector3(pos.x + devW, devH / 2, pos.z + devD / 2),
                new THREE.Vector3(X0 - 6, devH / 2, pos.z + devD / 2),
                new THREE.Vector3(X0, LANE_THICK + 1, pipeZ[pi]),
            ];
            const geo = new THREE.BufferGeometry().setFromPoints(pts);
            const mat = new THREE.LineBasicMaterial({
                color: 0x3a3731,
                transparent: true,
                opacity: 0.3,
            });
            const line = new THREE.Line(geo, mat);
            scene.add(line);
            wires.push({ line, deviceId: d.id, pipeId: d.pipeIn });
        });

        const fwdPaths: FwdPath[] = [];
        structuralDevices.forEach((d) => {
            if (!d.pipeIn) return;
            const pi = structuralPipelines.findIndex((p) => p.id === d.pipeIn);
            if (pi < 0) return;
            const pos = devicePositions[d.id];
            if (!pos) return;
            const pts = [
                new THREE.Vector3(pos.x + devW, devH / 2, pos.z + devD / 2),
                new THREE.Vector3(X0 - 6, devH / 2, pos.z + devD / 2),
                new THREE.Vector3(X0, LANE_THICK + 1, pipeZ[pi]),
                new THREE.Vector3(X0 + LANE_LEN, LANE_THICK + 1, pipeZ[pi]),
            ];
            const lens: number[] = [];
            let total = 0;
            for (let i = 1; i < pts.length; i++) {
                const l = pts[i].distanceTo(pts[i - 1]);
                lens.push(l);
                total += l;
            }
            fwdPaths.push({ pts, lens, total, density: 0.5, deviceId: d.id });
        });

        const fwdPS = buildParticles(fwdPaths, 0xFFC061, 2.4);
        if (fwdPS.points) scene.add(fwdPS.points);

        const sceneCenter = new THREE.Vector3(LANE_LEN / 2 - 40, 20, colSpan / 2);
        const sph = new THREE.Spherical(500, Math.PI / 3.2, -Math.PI / 4);
        camera.position.setFromSpherical(sph).add(sceneCenter);
        camera.lookAt(sceneCenter);

        const labelSources: Omit<LabelInfo, 'sx' | 'sy'>[] = [];
        Object.entries(deviceMeshes).forEach(([id, m]) => {
            const d = structuralDevices.find((x) => x.id === id);
            if (!d) return;
            labelSources.push({
                kind: 'device',
                id,
                anchor: 'right',
                worldPos: () => new THREE.Vector3(m.position.x - devW / 2 - 2, devH / 2, m.position.z),
                text: d.name,
                subtext: '',
                active: false,
            });
        });
        Object.entries(pipeMeshes).forEach(([id, m]) => {
            const p = structuralPipelines.find((x) => x.id === id);
            if (!p) return;
            labelSources.push({
                kind: 'pipeline',
                id,
                anchor: 'pipeline',
                worldPos: () =>
                    new THREE.Vector3(m.position.x - LANE_LEN / 2 - 4, 0, m.position.z),
                text: p.name,
                subtext: '',
                active: false,
            });
        });
        fnMeshes.forEach((fb) => {
            labelSources.push({
                kind: 'fn',
                id: fb.fnId,
                anchor: 'top',
                worldPos: () =>
                    new THREE.Vector3(
                        fb.mesh.position.x,
                        LANE_THICK + fb.mesh.scale.y * fb.baseH + 4,
                        fb.mesh.position.z,
                    ),
                text: fb.fnId.replace(/^fn:/, ''),
                subtext: '',
                active: false,
            });
        });

        threeRef.current = {
            scene,
            camera,
            renderer,
            pickGroup,
            deviceMeshes,
            pipeMeshes,
            fnMeshes,
            fwdPS,
            fwdPaths,
            labelSources,
            wires,
            sceneCenter,
        };

        const raycaster = new THREE.Raycaster();
        const mouse = new THREE.Vector2();

        const onPointerUp = (e: PointerEvent): void => {
            const rect = renderer.domElement.getBoundingClientRect();
            mouse.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
            mouse.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
            raycaster.setFromCamera(mouse, camera);
            const hits = raycaster.intersectObjects(pickGroup.children, false);
            if (hits.length) {
                const h = hits[0].object;
                const ud = h.userData as { kind?: string; id?: string };
                if (ud.kind && ud.id) {
                    setSelected({ kind: ud.kind as SelectedItem['kind'], id: ud.id });
                }
            }
        };
        renderer.domElement.addEventListener('pointerup', onPointerUp);

        let lastHoverCheck = 0;
        const onPointerMove = (e: PointerEvent): void => {
            const now = performance.now();
            if (now - lastHoverCheck < 50) return;
            lastHoverCheck = now;
            const rect = renderer.domElement.getBoundingClientRect();
            mouse.x = ((e.clientX - rect.left) / rect.width) * 2 - 1;
            mouse.y = -((e.clientY - rect.top) / rect.height) * 2 + 1;
            raycaster.setFromCamera(mouse, camera);
            const hits = raycaster.intersectObjects(pickGroup.children, false);
            renderer.domElement.style.cursor = hits.length > 0 ? 'pointer' : 'default';
        };
        renderer.domElement.addEventListener('pointermove', onPointerMove);

        let raf: number;
        let lastLabelTick = 0;
        const tmpV = new THREE.Vector3();

        const animate = (now: number): void => {
            const live = liveRef.current;

            let maxFnPps = 0;
            for (const fn of live.functionsById.values()) {
                if (fn.pps > maxFnPps) { maxFnPps = fn.pps; }
            }

            fnMeshes.forEach((fb) => {
                const liveFn = live.functionsById.get(fb.fnId);
                const pps = liveFn?.pps ?? 0;
                const active = pps > 0;
                const mat = fb.mesh.material as THREE.MeshLambertMaterial;
                mat.color.setHex(active ? fb.activeFill : 0x1a1816);
                (mat.emissive as THREE.Color).setHex(active ? fb.activeEmissive : 0x000000);
                mat.emissiveIntensity = active ? 0.4 : 0;
                ((fb.mesh.userData.edges as THREE.LineSegments).material as THREE.LineBasicMaterial)
                    .color.setHex(active ? fb.activeEdge : 0x3a3731);
                const denom = Math.max(maxFnPps, FN_REF_PPS);
                const norm = Math.min(1, Math.sqrt(pps / denom));
                const breathe = Math.sin(now * 0.0009 + fb.x * 0.05) * 0.05;
                const target = active ? 1 + norm * FN_MAX_GROWTH + breathe : 1;
                fb.mesh.scale.y += (target - fb.mesh.scale.y) * 0.06;
            });

            Object.entries(pipeMeshes).forEach(([id, mesh]) => {
                const livePipe = live.pipelinesById.get(id);
                const pps = livePipe?.pps ?? 0;
                const active = pps > 0;
                const intensity = Math.min(1, pps / 30e6);
                const color = active
                    ? new THREE.Color().setHSL(0.07, 0.45, 0.16 + intensity * 0.06).getHex()
                    : 0x1a1816;
                const edgeColor = active ? 0xFFC061 : 0x3a3731;
                const mat = mesh.material as THREE.MeshLambertMaterial;
                mat.color.setHex(color);
                const edgesMat = (mesh.userData.edges as THREE.LineSegments).material as THREE.LineBasicMaterial;
                edgesMat.color.setHex(edgeColor);
                edgesMat.opacity = active ? 0.7 : 0.5;
                mesh.userData.baseColor = color;
                mesh.userData.edgeColor = edgeColor;
            });

            Object.entries(deviceMeshes).forEach(([id, mesh]) => {
                const liveDev = live.devicesById.get(id);
                const active = liveDev?.status === 'ok' && (liveDev?.rxPps ?? 0) > 0;
                const isVlan = (mesh.userData as { isVlan?: boolean }).isVlan ?? false;
                const color = active ? (isVlan ? 0x4a607a : 0x70533a) : 0x1d1b18;
                const edgeColor = active ? (isVlan ? 0x88a8c4 : 0xFFC061) : 0x3a3731;
                const mat = mesh.material as THREE.MeshLambertMaterial;
                mat.color.setHex(color);
                ((mesh.userData.edges as THREE.LineSegments).material as THREE.LineBasicMaterial)
                    .color.setHex(edgeColor);
                mesh.userData.baseColor = color;
                mesh.userData.edgeColor = edgeColor;
            });

            wires.forEach((w) => {
                const liveDev = live.devicesById.get(w.deviceId);
                const active = liveDev?.status === 'ok' && (liveDev?.rxPps ?? 0) > 0;
                const wireMat = w.line.material as THREE.LineBasicMaterial;
                wireMat.color.setHex(active ? 0xFFC061 : 0x3a3731);
                wireMat.opacity = active ? 0.55 : 0.3;
            });

            if (fwdPS.points && fwdPS.geo) {
                const positions = fwdPS.geo.attributes['position'].array as Float32Array;

                const pathPps = fwdPaths.map((path) => {
                    const liveDev = live.devicesById.get(path.deviceId);
                    return liveDev?.rxPps ?? 0;
                });
                const currentMax = pathPps.reduce((m, v) => (v > m ? v : m), 0);
                const decayed = peakPpsRef.current * PARTICLE_REF_DECAY;
                const peak = Math.max(currentMax, decayed, PARTICLE_MIN_REF_PPS);
                peakPpsRef.current = peak;
                const logFloor = Math.log10(PARTICLE_MIN_REF_PPS);
                const logPeak = Math.log10(peak);
                const logSpan = Math.max(logPeak - logFloor, 1e-6);

                fwdPS.ps.forEach((p) => {
                    const path = fwdPaths[p.pi];
                    if (!path) return;
                    const pps = pathPps[p.pi];
                    const baseFraction = pps <= 0
                        ? 0
                        : Math.min(1, (Math.log10(Math.max(pps, PARTICLE_MIN_REF_PPS)) - logFloor) / logSpan);
                    const fraction = baseFraction > 0
                        ? Math.max(baseFraction, PARTICLE_MIN_VISIBLE_FRACTION)
                        : 0;

                    if (fraction > 0) {
                        if (p.lastFraction === 0) {
                            p.t = Math.random();
                        }
                        p.t += Math.min(PARTICLE_MAX_DT, p.speed * 4 * fraction);
                        if (p.t > 1) p.t -= 1;
                        p.lastFraction = fraction;
                    } else {
                        if (p.lastFraction === 0) {
                            positions[p.posIdx * 3 + 0] = 0;
                            positions[p.posIdx * 3 + 1] = -1000;
                            positions[p.posIdx * 3 + 2] = 0;
                            return;
                        }
                        p.t += Math.min(PARTICLE_MAX_DT, p.speed * 4 * p.lastFraction);
                        if (p.t > 1) {
                            p.lastFraction = 0;
                            positions[p.posIdx * 3 + 0] = 0;
                            positions[p.posIdx * 3 + 1] = -1000;
                            positions[p.posIdx * 3 + 2] = 0;
                            return;
                        }
                    }

                    const target = p.t * path.total;
                    let acc = 0;
                    let pt = path.pts[0];
                    for (let i = 1; i < path.pts.length; i++) {
                        const l = path.lens[i - 1];
                        if (acc + l >= target) {
                            const u = (target - acc) / l;
                            tmpV.copy(path.pts[i - 1]).lerp(path.pts[i], u);
                            pt = tmpV;
                            break;
                        }
                        acc += l;
                    }
                    positions[p.posIdx * 3 + 0] = pt.x;
                    positions[p.posIdx * 3 + 1] = pt.y;
                    positions[p.posIdx * 3 + 2] = pt.z;
                });
                fwdPS.geo.attributes['position'].needsUpdate = true;
            }

            renderer.render(scene, camera);

            if (now - lastLabelTick > 33) {
                lastLabelTick = now;
                const out: LabelInfo[] = [];
                const { w: vw, h: vh } = canvasSizeRef.current;
                labelSources.forEach((l) => {
                    const wp = l.worldPos();
                    wp.project(camera);
                    if (wp.z < -1 || wp.z > 1) return;
                    const sx = (wp.x * 0.5 + 0.5) * vw;
                    const sy = (-wp.y * 0.5 + 0.5) * vh;
                    if (sx < -50 || sx > vw + 50 || sy < -20 || sy > vh + 20) return;
                    let subtext = '';
                    let active = false;
                    if (l.kind === 'device') {
                        const liveDev = liveRef.current.devicesById.get(l.id);
                        subtext = `${fmtPps(liveDev?.rxPps ?? 0)} pps`;
                        active = liveDev?.status === 'ok';
                    } else if (l.kind === 'pipeline') {
                        const livePipe = liveRef.current.pipelinesById.get(l.id);
                        subtext = fmtPps(livePipe?.pps ?? 0);
                        active = livePipe?.status === 'ok';
                    } else if (l.kind === 'fn') {
                        const liveFn = liveRef.current.functionsById.get(l.id);
                        subtext = fmtPps(liveFn?.pps ?? 0);
                        active = liveFn?.status === 'ok';
                    }
                    out.push({ ...l, sx, sy, subtext, active });
                });
                setLabels(out);
            }

            raf = requestAnimationFrame(animate);
        };
        raf = requestAnimationFrame(animate);

        return () => {
            cancelled = true;
            cancelAnimationFrame(raf);
            renderer.domElement.removeEventListener('pointerup', onPointerUp);
            renderer.domElement.removeEventListener('pointermove', onPointerMove);
            scene.traverse((obj) => {
                if ('geometry' in obj && (obj as unknown as { geometry?: { dispose?: () => void } }).geometry?.dispose) {
                    (obj as unknown as { geometry: { dispose: () => void } }).geometry.dispose();
                }
                const material = (obj as unknown as { material?: unknown }).material;
                if (Array.isArray(material)) {
                    material.forEach((m: { dispose?: () => void }) => m.dispose?.());
                } else if (material && typeof (material as { dispose?: () => void }).dispose === 'function') {
                    (material as { dispose: () => void }).dispose();
                }
            });
            if (fwdPS.geo) fwdPS.geo.dispose();
            renderer.dispose();
            renderer.forceContextLoss?.();
            if (mount && mount.contains(renderer.domElement)) {
                mount.removeChild(renderer.domElement);
            }
            threeRef.current = {
                scene: null,
                camera: null,
                renderer: null,
                pickGroup: null,
                deviceMeshes: {},
                pipeMeshes: {},
                fnMeshes: [],
                fwdPS: { points: null, ps: [], geo: null },
                fwdPaths: [],
                labelSources: [],
                wires: [],
                sceneCenter: null,
            };
        };
    }, [webglAvailable, structuralDevices, structuralPipelines, structuralFunctions]);

    useEffect(() => {
        const r = threeRef.current;
        if (!r.pickGroup) return;

        const isRelated = (kind: string, id: string): boolean => {
            if (!selected) return true;
            if (selected.kind === kind && selected.id === id) return true;
            if (selected.kind === 'device') {
                const d = structuralDevices.find((x) => x.id === selected.id);
                if (!d) return false;
                if (kind === 'pipeline') return d.pipeIn === id || d.pipeOut === id;
                if (kind === 'fn') {
                    const pIn = structuralPipelines.find((p) => p.id === d.pipeIn);
                    const pOut = structuralPipelines.find((p) => p.id === d.pipeOut);
                    return (
                        (pIn !== undefined && pIn.fns.includes(id)) ||
                        (pOut !== undefined && pOut.fns.includes(id))
                    );
                }
                return false;
            }
            if (selected.kind === 'pipeline') {
                const p = structuralPipelines.find((x) => x.id === selected.id);
                if (!p) return false;
                if (kind === 'fn') return p.fns.includes(id);
                if (kind === 'device') {
                    return structuralDevices.some(
                        (d) => (d.pipeIn === selected.id || d.pipeOut === selected.id) && d.id === id,
                    );
                }
                return false;
            }
            if (selected.kind === 'fn') {
                if (kind === 'pipeline') {
                    return structuralPipelines.find((x) => x.id === id)?.fns.includes(selected.id) ?? false;
                }
                return false;
            }
            return false;
        };

        r.pickGroup.children.forEach((m) => {
            const mesh = m as THREE.Mesh;
            const ud = mesh.userData as {
                kind: string;
                id: string;
                baseColor: number;
                edgeColor: number;
                edges: THREE.LineSegments;
            };
            const rel = isRelated(ud.kind, ud.id);
            const isSel = selected ? ud.kind === selected.kind && ud.id === selected.id : false;
            const mat = mesh.material as THREE.MeshLambertMaterial;
            mat.opacity = selected ? (rel ? 0.95 : 0.18) : 0.95;
            if (ud.edges) {
                const edgeMat = ud.edges.material as THREE.LineBasicMaterial;
                edgeMat.opacity = selected
                    ? rel ? 0.95 : 0.10
                    : ud.edgeColor === 0x3a3731 ? 0.5 : 0.75;
                edgeMat.color.setHex(isSel ? 0xe8e4d8 : ud.edgeColor);
            }
            if (mat.emissive) {
                mat.emissive.setHex(
                    isSel ? 0xFFC061 : ud.kind === 'fn' && ud.baseColor === 0x5a4632 ? 0x4a3a22 : 0x000000,
                );
                mat.emissiveIntensity = isSel
                    ? 0.6
                    : ud.kind === 'fn' && ud.baseColor === 0x5a4632 ? 0.4 : 0;
            }
        });

        r.wires.forEach((w) => {
            const rel = isRelated('device', w.deviceId) && isRelated('pipeline', w.pipeId);
            (w.line.material as THREE.LineBasicMaterial).opacity = selected ? (rel ? 0.7 : 0.06) : 0.4;
        });
    }, [selected, structuralDevices, structuralPipelines, structuralFunctions]);

    const unsupported = webglAvailable === false || rendererError;
    const loading = webglAvailable === null;

    return (
        <div ref={containerRef} className="dash-scene-container">
            <div className="dash-scene-horizon" />
            {!unsupported && <div ref={canvasMountRef} className="dash-scene-canvas" />}
            {unsupported && (
                <div className="dash-scene-fallback">
                    <div className="dash-scene-fallback__title">3D view unavailable</div>
                    <div className="dash-scene-fallback__hint">
                        WebGL is required to render the isometric topology but could not be
                        initialised in this environment.
                    </div>
                    <div className="dash-scene-fallback__hint">
                        Use the standard{' '}
                        <a className="dash-scene-fallback__link" href="/builtin/inspect">
                            Inspect
                        </a>{' '}
                        view instead.
                    </div>
                </div>
            )}
            {loading && (
                <div className="dash-scene-fallback">
                    <div className="dash-scene-fallback__hint">loading 3d engine…</div>
                </div>
            )}
            <div className="dash-label-overlay">
                {!unsupported && labels.map((l, i) => (
                    <SceneLabel key={`${l.kind}-${l.id}-${i}`} l={l} />
                ))}
            </div>
            {selected && !unsupported && (
                <Inspector
                    selected={selected}
                    onClose={() => setSelected(null)}
                    structuralDevices={structuralDevices}
                    structuralPipelines={structuralPipelines}
                    structuralFunctions={structuralFunctions}
                    live={liveRef.current}
                    usage={usage}
                />
            )}
        </div>
    );
};

/** A projected HTML label for a scene object. */
const SceneLabel: React.FC<{ l: LabelInfo }> = ({ l }) => {
    if (l.kind === 'pipeline') {
        return (
            <div
                className={`dash-label--pipeline${l.active ? ' dash-label--pipeline-active' : ''}`}
                style={{ left: l.sx - 60, top: l.sy - 9 }}
            >
                <span
                    className="dash-dot"
                    style={{
                        width: 5,
                        height: 5,
                        background: l.active ? 'var(--iv-ok)' : 'var(--iv-idle)',
                    }}
                />
                <span>{l.text}</span>
            </div>
        );
    }
    if (l.kind === 'device') {
        return (
            <div
                className="dash-label--device"
                style={{ left: l.sx - 90, top: l.sy - 10 }}
            >
                <div>{l.text}</div>
                <div className="dash-label--device-sub">{l.subtext}</div>
            </div>
        );
    }
    if (l.kind === 'fn') {
        return (
            <div
                className="dash-label--fn"
                style={{ left: l.sx - 60, top: l.sy - 22 }}
            >
                <div className={l.active ? 'dash-label--fn-active' : 'dash-label--fn-idle'}>
                    {l.text}
                </div>
                <div className="dash-label--fn-sub">{l.subtext}</div>
            </div>
        );
    }
    return null;
};
