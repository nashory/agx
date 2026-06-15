import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { defaultPreferences } from '../../appLogic';
import type { Agent, RuntimeConfigInfo, RuntimeStatusInfo } from '../../types';
import { SettingsView } from './SettingsView';

const runtimeStatus: RuntimeStatusInfo = {
  running: true,
  pid: 123,
  version: 'test',
  uptimeSeconds: 65,
  socketPath: '/tmp/agx.sock',
  lockPath: '/tmp/agx.lock',
  recovery: {},
};

const agents: Agent[] = [
  { name: 'codex', command: 'codex', description: 'Codex', available: true },
  { name: 'gemini', command: 'gemini', description: 'Gemini', available: true },
];

function renderSettings(overrides: Partial<{
  runtimeConfig: RuntimeConfigInfo;
  onDefaultAgentChange: (agentName: string) => Promise<void>;
}> = {}) {
  const onDefaultAgentChange = overrides.onDefaultAgentChange ?? vi.fn().mockResolvedValue(undefined);

  render(
    <SettingsView
      preferences={defaultPreferences}
      onPreferencesChange={vi.fn()}
      theme="dark"
      onThemeChange={vi.fn()}
      onToggleTheme={vi.fn()}
      onResetDatabase={vi.fn().mockResolvedValue(undefined)}
      runtimeStatus={runtimeStatus}
      runtimeConfig={overrides.runtimeConfig ?? { defaultAgent: 'codex' }}
      agents={agents}
      onDefaultAgentChange={onDefaultAgentChange}
      onRefreshRuntime={vi.fn().mockResolvedValue(runtimeStatus)}
      onStartRuntime={vi.fn().mockResolvedValue(runtimeStatus)}
      onInstallRuntimeService={vi.fn().mockResolvedValue(runtimeStatus)}
      onStopRuntime={vi.fn().mockResolvedValue(runtimeStatus)}
      busy={false}
    />,
  );

  return { onDefaultAgentChange };
}

describe('SettingsView', () => {
  it('saves default agent changes through the provided runtime action', async () => {
    const user = userEvent.setup();
    const onDefaultAgentChange = vi.fn().mockResolvedValue(undefined);
    renderSettings({ onDefaultAgentChange });

    const select = screen.getByDisplayValue('Codex') as HTMLSelectElement;
    await user.selectOptions(select, 'gemini');

    await waitFor(() => expect(onDefaultAgentChange).toHaveBeenCalledWith('gemini'));
  });

  it('shows an unavailable configured default agent instead of silently replacing it', () => {
    renderSettings({ runtimeConfig: { defaultAgent: 'opencode' } });

    expect(screen.getByDisplayValue('OpenCode (not installed)')).not.toBeNull();
  });
});
