import React from 'react';

interface SceneErrorBoundaryProps {
    children: React.ReactNode;
}

interface SceneErrorBoundaryState {
    hasError: boolean;
}

/**
 * Catches unhandled exceptions thrown by the three.js scene so a WebGL
 * failure or runtime crash does not bring down the rest of the page.
 */
export class SceneErrorBoundary extends React.Component<
    SceneErrorBoundaryProps,
    SceneErrorBoundaryState
> {
    state: SceneErrorBoundaryState = { hasError: false };

    static getDerivedStateFromError(): SceneErrorBoundaryState {
        return { hasError: true };
    }

    componentDidCatch(error: Error): void {
        console.error('IsoScene3D crashed:', error);
    }

    render(): React.ReactNode {
        if (this.state.hasError) {
            return (
                <div className="dash-scene-container">
                    <div className="dash-scene-fallback">
                        <div className="dash-scene-fallback__title">3D view unavailable</div>
                        <div className="dash-scene-fallback__hint">
                            The 3D scene failed to initialise. WebGL may not be
                            supported in this environment.
                        </div>
                        <div className="dash-scene-fallback__hint">
                            Use the standard{' '}
                            <a className="dash-scene-fallback__link" href="/builtin/inspect">
                                Inspect
                            </a>{' '}
                            view instead.
                        </div>
                    </div>
                </div>
            );
        }
        return this.props.children;
    }
}
