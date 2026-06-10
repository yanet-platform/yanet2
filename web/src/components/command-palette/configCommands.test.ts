import { describe, it, expect, vi } from 'vitest';
import { buildConfigCommands } from './configCommands';

describe('buildConfigCommands', () => {
    it('acl parameterization — produces the correct trio', () => {
        const onAddConfig = vi.fn();
        const onDeleteConfig = vi.fn();
        const onSwitchConfig = vi.fn();

        const result = buildConfigCommands({
            currentConfig: 'main',
            draftConfigs: ['main', 'staging'],
            dirtySet: new Set<string>(),
            addConfigSub: 'Create a new ACL configuration',
            withKeywords: true,
            onAddConfig,
            onDeleteConfig,
            onSwitchConfig,
        });

        expect(result).toHaveLength(3);

        expect(result[0]).toMatchObject({
            id: '__add_config',
            icon: '▤',
            label: 'Add config',
            sub: 'Create a new ACL configuration',
            keywords: 'add config create new',
            onSelect: expect.any(Function),
        });

        expect(result[1]).toMatchObject({
            id: '__delete_config',
            icon: '✕',
            label: 'Delete config',
            sub: 'Delete "main"',
            keywords: 'delete remove config',
            onSelect: expect.any(Function),
        });

        expect(result[2]).toMatchObject({
            id: '__config_staging',
            icon: '⇥',
            label: 'Switch to config staging',
            sub: undefined,
            keywords: 'switch config tab staging',
            onSelect: expect.any(Function),
        });
    });

    it('forward parameterization — produces the correct trio', () => {
        const onAddConfig = vi.fn();
        const onDeleteConfig = vi.fn();
        const onSwitchConfig = vi.fn();

        const result = buildConfigCommands({
            currentConfig: 'prod',
            draftConfigs: ['prod', 'dev'],
            dirtySet: new Set(['dev']),
            addConfigSub: 'Create a new forward configuration',
            withKeywords: true,
            onAddConfig,
            onDeleteConfig,
            onSwitchConfig,
        });

        expect(result).toHaveLength(3);

        expect(result[0]).toMatchObject({
            id: '__add_config',
            sub: 'Create a new forward configuration',
            keywords: 'add config create new',
        });

        expect(result[1]).toMatchObject({
            id: '__delete_config',
            sub: 'Delete "prod"',
            keywords: 'delete remove config',
        });

        expect(result[2]).toMatchObject({
            id: '__config_dev',
            label: 'Switch to config dev',
            sub: 'unsaved changes',
            keywords: 'switch config tab dev',
        });
    });

    it('decap parameterization — produces the correct trio', () => {
        const onAddConfig = vi.fn();
        const onDeleteConfig = vi.fn();
        const onSwitchConfig = vi.fn();

        const result = buildConfigCommands({
            currentConfig: 'main',
            draftConfigs: ['main', 'backup'],
            dirtySet: new Set<string>(),
            addConfigSub: 'Create a new decap configuration',
            withKeywords: true,
            onAddConfig,
            onDeleteConfig,
            onSwitchConfig,
        });

        expect(result).toHaveLength(3);

        expect(result[0]).toMatchObject({
            id: '__add_config',
            sub: 'Create a new decap configuration',
            keywords: 'add config create new',
        });

        expect(result[1]).toMatchObject({
            id: '__delete_config',
            sub: 'Delete "main"',
            keywords: 'delete remove config',
        });

        expect(result[2]).toMatchObject({
            id: '__config_backup',
            keywords: 'switch config tab backup',
        });
    });

    it('route parameterization — no sub/keywords on Add, no keywords on Delete, switch has keywords', () => {
        const onAddConfig = vi.fn();
        const onDeleteConfig = vi.fn();
        const onSwitchConfig = vi.fn();

        const result = buildConfigCommands({
            currentConfig: 'main',
            draftConfigs: ['main', 'canary'],
            dirtySet: new Set<string>(),
            onAddConfig,
            onDeleteConfig,
            onSwitchConfig,
        });

        expect(result).toHaveLength(3);

        expect(result[0].sub).toBeUndefined();
        expect(result[0].keywords).toBeUndefined();

        expect(result[1].sub).toBe('Delete "main"');
        expect(result[1].keywords).toBeUndefined();

        expect(result[2]).toMatchObject({
            id: '__config_canary',
            keywords: 'switch config tab canary',
            sub: undefined,
        });
    });

    it('omits the Delete item when currentConfig is empty', () => {
        const result = buildConfigCommands({
            currentConfig: '',
            draftConfigs: ['alpha', 'beta'],
            dirtySet: new Set<string>(),
            withKeywords: true,
            onAddConfig: vi.fn(),
            onDeleteConfig: vi.fn(),
            onSwitchConfig: vi.fn(),
        });

        expect(result.find((c) => c.id === '__delete_config')).toBeUndefined();
    });

    it('excludes the active config from switch items', () => {
        const result = buildConfigCommands({
            currentConfig: 'alpha',
            draftConfigs: ['alpha', 'beta', 'gamma'],
            dirtySet: new Set<string>(),
            withKeywords: true,
            onAddConfig: vi.fn(),
            onDeleteConfig: vi.fn(),
            onSwitchConfig: vi.fn(),
        });

        const switchIds = result.filter((c) => c.id.startsWith('__config_')).map((c) => c.id);
        expect(switchIds).toEqual(['__config_beta', '__config_gamma']);
        expect(switchIds).not.toContain('__config_alpha');
    });

    it('marks dirty switch items with "unsaved changes" sub', () => {
        const result = buildConfigCommands({
            currentConfig: 'clean',
            draftConfigs: ['clean', 'dirty'],
            dirtySet: new Set(['dirty']),
            withKeywords: true,
            onAddConfig: vi.fn(),
            onDeleteConfig: vi.fn(),
            onSwitchConfig: vi.fn(),
        });

        const switchItem = result.find((c) => c.id === '__config_dirty');
        expect(switchItem?.sub).toBe('unsaved changes');
    });

    it('leaves sub undefined for non-dirty switch items', () => {
        const result = buildConfigCommands({
            currentConfig: 'x',
            draftConfigs: ['x', 'y'],
            dirtySet: new Set<string>(),
            withKeywords: true,
            onAddConfig: vi.fn(),
            onDeleteConfig: vi.fn(),
            onSwitchConfig: vi.fn(),
        });

        const switchItem = result.find((c) => c.id === '__config_y');
        expect(switchItem?.sub).toBeUndefined();
    });

    it('onSelect on Add invokes onAddConfig', () => {
        const onAddConfig = vi.fn();
        const result = buildConfigCommands({
            currentConfig: '',
            draftConfigs: [],
            dirtySet: new Set<string>(),
            onAddConfig,
            onDeleteConfig: vi.fn(),
            onSwitchConfig: vi.fn(),
        });

        result[0].onSelect();
        expect(onAddConfig).toHaveBeenCalledOnce();
    });

    it('onSelect on Delete invokes onDeleteConfig', () => {
        const onDeleteConfig = vi.fn();
        const result = buildConfigCommands({
            currentConfig: 'main',
            draftConfigs: ['main'],
            dirtySet: new Set<string>(),
            onAddConfig: vi.fn(),
            onDeleteConfig,
            onSwitchConfig: vi.fn(),
        });

        const deleteItem = result.find((c) => c.id === '__delete_config')!;
        deleteItem.onSelect();
        expect(onDeleteConfig).toHaveBeenCalledOnce();
    });

    it('onSelect on a switch item invokes onSwitchConfig with the correct name', () => {
        const onSwitchConfig = vi.fn();
        const result = buildConfigCommands({
            currentConfig: 'a',
            draftConfigs: ['a', 'b', 'c'],
            dirtySet: new Set<string>(),
            onAddConfig: vi.fn(),
            onDeleteConfig: vi.fn(),
            onSwitchConfig,
        });

        const switchB = result.find((c) => c.id === '__config_b')!;
        switchB.onSelect();
        expect(onSwitchConfig).toHaveBeenCalledWith('b');

        const switchC = result.find((c) => c.id === '__config_c')!;
        switchC.onSelect();
        expect(onSwitchConfig).toHaveBeenCalledWith('c');
    });
});
