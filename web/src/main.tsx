import React from 'react';
import ReactDOM from 'react-dom/client';
import { ThemeProvider } from '@gravity-ui/uikit';
import { ToasterProvider, ToasterComponent } from '@gravity-ui/uikit';
import { toaster } from '@gravity-ui/uikit/toaster-singleton';
import App from './App';
import '@gravity-ui/uikit/styles/fonts.css';
import '@gravity-ui/uikit/styles/styles.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
        <ThemeProvider theme="dark">
            <ToasterProvider toaster={toaster}>
                <App />
                <ToasterComponent />
            </ToasterProvider>
        </ThemeProvider>
    </React.StrictMode>
);

