import React from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { FatalErrorBoundary } from './FatalErrorBoundary';
import { installGlobalErrorDiagnostics } from './frontendDiagnostics';
import { api } from './api';

installGlobalErrorDiagnostics();
void api.FrontendHeartbeat();
window.setInterval(() => void api.FrontendHeartbeat(), 5_000);

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <FatalErrorBoundary>
      <App />
    </FatalErrorBoundary>
  </React.StrictMode>,
);
