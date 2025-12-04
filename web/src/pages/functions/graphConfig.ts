import { ECanChangeBlockGeometry } from '@gravity-ui/graph';
import type { HookGraphParams } from '@gravity-ui/graph/react';

export const graphConfig: HookGraphParams = {
    viewConfiguration: {
        colors: {
            selection: {
                background: 'rgba(255, 190, 92, 0.1)',
                border: 'rgba(255, 190, 92, 1)',
            },
            connection: {
                background: 'rgba(255, 255, 255, 0.5)',
                selectedBackground: 'rgba(234, 201, 74, 1)',
            },
            block: {
                background: 'rgba(37, 27, 37, 1)',
                border: 'rgba(229, 229, 229, 0.2)',
                selectedBorder: 'rgba(255, 190, 92, 1)',
                text: 'rgba(255, 255, 255, 1)',
            },
            anchor: {
                background: 'rgba(255, 190, 92, 1)',
            },
            canvas: {
                layerBackground: 'rgba(22, 13, 27, 1)',
                belowLayerBackground: 'rgba(22, 13, 27, 1)',
                dots: 'rgba(255, 255, 255, 0.2)',
                border: 'rgba(255, 255, 255, 0.3)',
            },
        },
        constants: {
            block: {
                SCALES: [0.1, 0.2, 0.5],
            },
        },
    },
    settings: {
        canDragCamera: true,
        canZoomCamera: false, // Disable zoom to not intercept scroll
        canDuplicateBlocks: false,
        canChangeBlockGeometry: ECanChangeBlockGeometry.ALL,
        canCreateNewConnections: true,
        showConnectionArrows: false,
        scaleFontSize: 1,
        useBezierConnections: true,
        useBlocksAnchors: true,
        showConnectionLabels: false,
        blockComponents: {},
    },
};

