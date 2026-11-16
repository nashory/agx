const diagnosticKey = 'agx-frontend-diagnostics';
const reloadKey = 'agx-frontend-recovery-at';
const maxDiagnostics = 20;
const reloadCooldownMs = 60_000;

export type FrontendDiagnostic = {
  at: string;
  kind: string;
  message: string;
  stack?: string;
};

function errorDetails(value: unknown): { message: string; stack?: string } {
  if (value instanceof Error) return { message: value.message, stack: value.stack };
  if (typeof value === 'string') return { message: value };
  try {
    return { message: JSON.stringify(value) };
  } catch {
    return { message: String(value) };
  }
}

export function recordFrontendDiagnostic(kind: string, value: unknown) {
  try {
    const details = errorDetails(value);
    const current = JSON.parse(localStorage.getItem(diagnosticKey) ?? '[]');
    const entries: FrontendDiagnostic[] = Array.isArray(current) ? current : [];
    entries.push({ at: new Date().toISOString(), kind, ...details });
    localStorage.setItem(diagnosticKey, JSON.stringify(entries.slice(-maxDiagnostics)));
  } catch {
    // Diagnostics must never become another startup failure.
  }
}

export function claimAutomaticReload(now = Date.now()): boolean {
  try {
    const stored = sessionStorage.getItem(reloadKey);
    const previous = stored === null ? Number.NaN : Number(stored);
    if (Number.isFinite(previous) && now - previous < reloadCooldownMs) return false;
    sessionStorage.setItem(reloadKey, String(now));
    return true;
  } catch {
    return false;
  }
}

export function installGlobalErrorDiagnostics() {
  window.addEventListener('error', (event) => {
    recordFrontendDiagnostic('window.error', event.error ?? event.message);
  });
  window.addEventListener('unhandledrejection', (event) => {
    recordFrontendDiagnostic('unhandledrejection', event.reason);
  });
}

