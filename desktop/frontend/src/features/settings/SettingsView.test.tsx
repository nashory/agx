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
  transport: 'unix /tmp/agx.sock',
  recovery: {},
};

const agents: Agent[] = [
  { name: 'codex', command: 'codex', description: 'Codex', available: true },
  { name: 'gemini', command: 'gemini', description: 'Gemini', available: true },
];

function renderSettings(overrides: Partial<{
  runtimeConfig: RuntimeConfigInfo;
  onDefaultAgentChange: (agentName: string) => Promise<void>;
  onVoiceSTTChange: (voiceStt: RuntimeConfigInfo['voiceStt']) => Promise<void>;
  onVoiceSTTSetup: () => Promise<void>;
}> = {}) {
  const onDefaultAgentChange = overrides.onDefaultAgentChange ?? vi.fn().mockResolvedValue(undefined);
  const onVoiceSTTChange = overrides.onVoiceSTTChange ?? vi.fn().mockResolvedValue(undefined);
  const onVoiceSTTSetup = overrides.onVoiceSTTSetup ?? vi.fn().mockResolvedValue(undefined);

  render(
    <SettingsView
      preferences={defaultPreferences}
      onPreferencesChange={vi.fn()}
      theme="dark"
      onThemeChange={vi.fn()}
      onToggleTheme={vi.fn()}
      onResetDatabase={vi.fn().mockResolvedValue(undefined)}
      runtimeStatus={runtimeStatus}
      runtimeConfig={overrides.runtimeConfig ?? { defaultAgent: 'codex', voiceStt: { mode: 'auto', ffmpegPath: '', whisperPath: '', modelPath: '', language: 'auto', timeout: '60s' } }}
      agents={agents}
      onDefaultAgentChange={onDefaultAgentChange}
      onVoiceSTTChange={onVoiceSTTChange}
      onVoiceSTTSetup={onVoiceSTTSetup}
      onRefreshRuntime={vi.fn().mockResolvedValue(runtimeStatus)}
      onStartRuntime={vi.fn().mockResolvedValue(runtimeStatus)}
      onInstallRuntimeService={vi.fn().mockResolvedValue(runtimeStatus)}
      onStopRuntime={vi.fn().mockResolvedValue(runtimeStatus)}
      busy={false}
    />,
  );

  return { onDefaultAgentChange, onVoiceSTTChange, onVoiceSTTSetup };
}

describe('SettingsView', () => {
  it('saves default agent changes through the provided runtime action', async () => {
    const user = userEvent.setup();
    const onDefaultAgentChange = vi.fn().mockResolvedValue(undefined);
    renderSettings({ onDefaultAgentChange });

    const select = screen.getByRole('combobox', { name: 'Default agent' });
    await user.click(select);
    await user.click(screen.getByRole('option', { name: 'Gemini CLI' }));

    await waitFor(() => expect(onDefaultAgentChange).toHaveBeenCalledWith('gemini'));
  });

  it('disables the default agent picker while the save action is pending', async () => {
    const user = userEvent.setup();
    let resolveSave: (() => void) | undefined;
    const onDefaultAgentChange = vi.fn().mockReturnValue(new Promise<void>((resolve) => {
      resolveSave = resolve;
    }));
    renderSettings({ onDefaultAgentChange });

    const select = screen.getByRole('combobox', { name: 'Default agent' });
    await user.click(select);
    await user.click(screen.getByRole('option', { name: 'Gemini CLI' }));

    await waitFor(() => expect(select).toBeDisabled());
    resolveSave?.();
    await waitFor(() => expect(select).not.toBeDisabled());
  });

  it('shows an unavailable configured default agent instead of silently replacing it', () => {
    renderSettings({ runtimeConfig: { defaultAgent: 'local-agent', voiceStt: { mode: 'auto', ffmpegPath: '', whisperPath: '', modelPath: '', language: 'auto', timeout: '60s' } } });

    expect(screen.getByRole('combobox', { name: 'Default agent' })).toHaveTextContent('local-agent (not installed)');
  });

  it('shows the runtime transport instead of assuming a Unix socket', () => {
    renderSettings();

    expect(screen.getByText('Transport')).not.toBeNull();
    expect(screen.getByText('unix /tmp/agx.sock')).not.toBeNull();
    expect(screen.queryByText('Socket')).toBeNull();
  });

  it('saves optional voice STT settings', async () => {
    const user = userEvent.setup();
    const onVoiceSTTChange = vi.fn().mockResolvedValue(undefined);
    renderSettings({ onVoiceSTTChange });

    await user.click(screen.getByRole('combobox', { name: 'Voice transcription mode' }));
    await user.click(screen.getByRole('option', { name: 'Enabled' }));
    await user.type(screen.getByPlaceholderText('ffmpeg'), 'ffmpeg');
    await user.type(screen.getByPlaceholderText('whisper-cli'), 'whisper-cli');
    await user.type(screen.getByPlaceholderText('Auto'), '/models/base.bin');
    await user.click(screen.getByRole('combobox', { name: 'Voice transcription language' }));
    await user.click(screen.getByRole('option', { name: 'Korean' }));
    await user.clear(screen.getByPlaceholderText('60s'));
    await user.type(screen.getByPlaceholderText('60s'), '90s');
    await user.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => expect(onVoiceSTTChange).toHaveBeenCalledWith({
      mode: 'enabled',
      ffmpegPath: 'ffmpeg',
      whisperPath: 'whisper-cli',
      modelPath: '/models/base.bin',
      language: 'ko',
      timeout: '90s',
    }));
  });

  it('starts the shared voice STT setup flow', async () => {
    const user = userEvent.setup();
    const onVoiceSTTSetup = vi.fn().mockResolvedValue(undefined);
    renderSettings({ onVoiceSTTSetup });

    await user.click(screen.getByRole('button', { name: /setup/i }));

    await waitFor(() => expect(onVoiceSTTSetup).toHaveBeenCalled());
  });
});
