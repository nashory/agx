import { claimAutomaticReload, recordFrontendDiagnostic } from './frontendDiagnostics';
import { describe, expect, it } from 'vitest';

describe('frontend diagnostics', () => {
  it('records errors without throwing', () => {
    recordFrontendDiagnostic('test', new Error('render failed'));
    expect(localStorage.getItem('agx-frontend-diagnostics')).toContain('render failed');
  });

  it('allows only one automatic reload during the cooldown', () => {
    expect(claimAutomaticReload(10_000)).toBe(true);
    expect(claimAutomaticReload(20_000)).toBe(false);
    expect(claimAutomaticReload(80_000)).toBe(true);
  });
});

