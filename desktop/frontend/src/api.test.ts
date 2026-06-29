import { describe, expect, it, vi } from 'vitest';

import { api } from './api';
import type { DiscordStatusInfo, RuntimeConfigInfo } from './types';
import { installWailsAppMock } from './test/wails';

const discordStatus: DiscordStatusInfo = {
  enabled: true,
  connected: true,
  uptimeSeconds: 10,
  sync: { running: false },
};

describe('api bridge', () => {
  it('falls back to safe defaults when Wails is unavailable', async () => {
    await expect(api.ListProjects()).resolves.toHaveLength(1);
    await expect(api.RuntimeConfig()).resolves.toEqual({
      defaultAgent: 'codex',
      voiceStt: { mode: 'auto', ffmpegPath: '', whisperPath: '', modelPath: '', language: 'auto', timeout: '60s' },
    });
    await expect(api.DiscordStatus()).resolves.toEqual({
      enabled: false,
      connected: false,
      uptimeSeconds: 0,
      sync: { running: false },
    });
    await expect(api.ComposePrompt('fix it', ['a.ts', '', 'a.ts', 'b.ts'])).resolves.toBe(
      'Read these files first and use them as context: a.ts, b.ts\nfix it',
    );
  });

  it('delegates default agent changes to Wails and preserves returned config', async () => {
    const returned: RuntimeConfigInfo = {
      defaultAgent: 'gemini',
      voiceStt: { mode: 'auto', ffmpegPath: '', whisperPath: '', modelPath: '', language: 'auto', timeout: '60s' },
    };
    const UpdateDefaultAgent = vi.fn().mockResolvedValue(returned);
    installWailsAppMock({ UpdateDefaultAgent });

    await expect(api.UpdateDefaultAgent('gemini')).resolves.toBe(returned);

    expect(UpdateDefaultAgent).toHaveBeenCalledWith('gemini');
  });

  it('delegates voice STT changes to Wails', async () => {
    const returned: RuntimeConfigInfo = {
      defaultAgent: 'codex',
      voiceStt: { mode: 'enabled', ffmpegPath: 'ffmpeg', whisperPath: 'whisper-cli', modelPath: '/models/base.bin', language: 'ko', timeout: '90s' },
    };
    const UpdateVoiceSTT = vi.fn().mockResolvedValue(returned);
    installWailsAppMock({ UpdateVoiceSTT });

    await expect(api.UpdateVoiceSTT('enabled', 'ffmpeg', 'whisper-cli', '/models/base.bin', 'ko', '90s')).resolves.toBe(returned);

    expect(UpdateVoiceSTT).toHaveBeenCalledWith('enabled', 'ffmpeg', 'whisper-cli', '/models/base.bin', 'ko', '90s');
  });

  it('trims Discord allowed user ID before calling Wails connect', async () => {
    const DiscordConnect = vi.fn().mockResolvedValue(discordStatus);
    installWailsAppMock({ DiscordConnect });

    await expect(api.DiscordConnect('token', 'guild', '  user  ')).resolves.toBe(discordStatus);

    expect(DiscordConnect).toHaveBeenCalledWith('token', 'guild', 'user');
  });

  it('uses modern Discord sync methods before legacy fallbacks', async () => {
    const DiscordSoftSync = vi.fn().mockResolvedValue(discordStatus);
    const DiscordSync = vi.fn().mockRejectedValue(new Error('legacy should not run'));
    const DiscordHardSync = vi.fn().mockResolvedValue(discordStatus);
    const DiscordResetManagedChannels = vi.fn().mockRejectedValue(new Error('legacy reset should not run'));
    installWailsAppMock({
      DiscordSoftSync,
      DiscordSync,
      DiscordHardSync,
      DiscordResetManagedChannels,
    });

    await expect(api.DiscordSoftSync()).resolves.toBe(discordStatus);
    await expect(api.DiscordHardSync()).resolves.toBe(discordStatus);

    expect(DiscordSoftSync).toHaveBeenCalledTimes(1);
    expect(DiscordSync).not.toHaveBeenCalled();
    expect(DiscordHardSync).toHaveBeenCalledTimes(1);
    expect(DiscordResetManagedChannels).not.toHaveBeenCalled();
  });

  it('falls back to legacy Discord sync method names when needed', async () => {
    const DiscordSync = vi.fn().mockResolvedValue(discordStatus);
    const DiscordResetManagedChannels = vi.fn().mockResolvedValue(discordStatus);
    installWailsAppMock({
      DiscordSync,
      DiscordResetManagedChannels,
    });

    await expect(api.DiscordSoftSync()).resolves.toBe(discordStatus);
    await expect(api.DiscordHardSync()).resolves.toBe(discordStatus);

    expect(DiscordSync).toHaveBeenCalledTimes(1);
    expect(DiscordResetManagedChannels).toHaveBeenCalledTimes(1);
  });

  it('reference-counts shared log streams for multiple terminal consumers', async () => {
    const StartLogStream = vi.fn().mockResolvedValue(undefined);
    const StopLogStream = vi.fn().mockResolvedValue(undefined);
    installWailsAppMock({ StartLogStream, StopLogStream });

    await api.StartLogStream('task-api-test', 100);
    await api.StartLogStream('task-api-test', 100);
    await api.StopLogStream('task-api-test');

    expect(StartLogStream).toHaveBeenCalledTimes(2);
    expect(StopLogStream).not.toHaveBeenCalled();

    await api.StopLogStream('task-api-test');

    expect(StopLogStream).toHaveBeenCalledWith('task-api-test');
  });

  it('rolls back shared log stream reference counts when start fails', async () => {
    const StartLogStream = vi
      .fn()
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('stream failed'));
    const StopLogStream = vi.fn().mockResolvedValue(undefined);
    installWailsAppMock({ StartLogStream, StopLogStream });

    await api.StartLogStream('task-api-failure', 100);
    await expect(api.StartLogStream('task-api-failure', 100)).rejects.toThrow('stream failed');
    await api.StopLogStream('task-api-failure');

    expect(StopLogStream).toHaveBeenCalledWith('task-api-failure');
  });
});
