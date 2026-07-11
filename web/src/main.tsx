import React from 'react';
import ReactDOM from 'react-dom/client';
import { ThemeProvider } from '@gravity-ui/uikit';
import { ToasterProvider, ToasterComponent } from '@gravity-ui/uikit';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import App from './App';
import { loadRuntimeConfig } from '@yanet/core/gateways/runtimeConfig';
import '@gravity-ui/uikit/styles/fonts.css';
import '@gravity-ui/uikit/styles/styles.css';
import '@yanet/core/styles/tokens.scss';

loadRuntimeConfig().then((runtimeConfig) => {
    ReactDOM.createRoot(document.getElementById('root')!).render(
        <React.StrictMode>
            <ThemeProvider theme="dark">
                <ToasterProvider toaster={toaster}>
                    <App runtimeConfig={runtimeConfig} />
                    <ToasterComponent />
                </ToasterProvider>
            </ThemeProvider>
        </React.StrictMode>
    );
});
