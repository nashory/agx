import React from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { FatalErrorBoundary } from './FatalErrorBoundary';
import { installGlobalErrorDiagnostics } from './frontendDiagnostics';

installGlobalErrorDiagnostics();

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <FatalErrorBoundary>
      <App />
    </FatalErrorBoundary>
  </React.StrictMode>,
);
