import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import type { DiscordStatusInfo } from '../../types';
import { api } from '../../api';
import { DiscordView } from './DiscordView';

vi.mock('../../api', () => ({
  api: {
    DiscordConnect: vi.fn(),
    DiscordDisconnect: vi.fn(),
    DiscordSoftSync: vi.fn(),
    DiscordHardSync: vi.fn(),
    OpenDiscordInvite: vi.fn(),
  },
}));

const disconnectedStatus: DiscordStatusInfo = {
  enabled: false,
  connected: false,
  uptimeSeconds: 0,
  sync: { running: false },
};

const connectedStatus: DiscordStatusInfo = {
  enabled: true,
  connected: true,
  guildId: 'guild-1',
  guildName: 'AGX Test',
  allowedUserIds: ['user-1'],
  maskedBotToken: '••••token',
  uptimeSeconds: 12,
  sync: { running: false },
};

function renderDiscord(initialStatus: DiscordStatusInfo) {
  const onLog = vi.fn();
  const onError = vi.fn();

  function Harness() {
    const [status, setStatus] = useState(initialStatus);
    return (
      <DiscordView
        status={status}
        statusLoading={false}
        onStatus={setStatus}
        onRefresh={vi.fn().mockResolvedValue(undefined)}
        onLog={onLog}
        onError={onError}
        theme="dark"
        onToggleTheme={vi.fn()}
      />
    );
  }

  render(<Harness />);
  return { onLog, onError };
}

describe('DiscordView', () => {
  it('requires a bot token before connecting from a disconnected state', () => {
    renderDiscord(disconnectedStatus);

    expect(screen.getByPlaceholderText('Discord bot token')).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
  });

  it('keeps connect disabled until required connection fields are present', async () => {
    const user = userEvent.setup();
    renderDiscord(disconnectedStatus);

    await user.type(screen.getByPlaceholderText('Discord bot token'), 'token');
    await user.type(screen.getByPlaceholderText('123456789012345678'), 'user-1');

    expect(api.DiscordConnect).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
  });

  it('does not allow reconnecting with a stale token after disconnect clears status', async () => {
    const user = userEvent.setup();
    vi.mocked(api.DiscordDisconnect).mockResolvedValue(disconnectedStatus);
    renderDiscord(connectedStatus);

    await user.click(screen.getByRole('button', { name: 'Disconnect' }));

    await waitFor(() => expect(api.DiscordDisconnect).toHaveBeenCalled());
    expect(screen.getByPlaceholderText('Discord bot token')).not.toHaveValue();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeDisabled();
  });

  it('soft syncs Discord and forwards the returned status', async () => {
    const user = userEvent.setup();
    const syncedStatus = { ...connectedStatus, sync: { running: false, stage: 'synced' } };
    vi.mocked(api.DiscordSoftSync).mockResolvedValue(syncedStatus);
    const { onLog } = renderDiscord(connectedStatus);

    await user.click(screen.getByRole('button', { name: 'Soft Sync' }));

    await waitFor(() => expect(api.DiscordSoftSync).toHaveBeenCalled());
    expect(onLog).toHaveBeenCalledWith('[ok] discord soft sync');
    expect(screen.getByText('synced')).not.toBeNull();
  });

  it('shows connected status errors as sync warnings instead of connection failures', () => {
    renderDiscord({ ...connectedStatus, error: 'context deadline exceeded' });

    expect(screen.getByText('connected')).not.toBeNull();
    expect(screen.getByText('Connected to AGX Test')).not.toBeNull();
    expect(screen.queryByText('Connection failed')).toBeNull();
    expect(screen.getByText('Last sync issue')).not.toBeNull();
    expect(screen.getByText('context deadline exceeded')).not.toBeNull();
  });

  it('surfaces hard sync API failures through the view error callback', async () => {
    const user = userEvent.setup();
    const error = new Error('sync timeout');
    vi.mocked(api.DiscordHardSync).mockRejectedValue(error);
    const { onError } = renderDiscord(connectedStatus);

    await user.click(screen.getByRole('button', { name: 'Hard Sync' }));
    await user.click(screen.getAllByRole('button', { name: 'Hard Sync' })[1]);

    await waitFor(() => expect(onError).toHaveBeenCalledWith('sync timeout'));
  });
});
