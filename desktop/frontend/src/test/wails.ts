import type { WailsApp } from '../api';

export function installWailsAppMock(overrides: Partial<WailsApp> = {}): Partial<WailsApp> {
  window.go = {
    desktop: {
      App: overrides as WailsApp,
    },
  };
  return overrides;
}
