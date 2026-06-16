import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { api } from '../../api';
import { DiscordTaskSyncAction } from './DiscordTaskSyncAction';

vi.mock('../../api', () => ({
  api: {
    DiscordTaskSync: vi.fn(),
  },
}));

function renderAction(overrides?: Partial<React.ComponentProps<typeof DiscordTaskSyncAction>>) {
  const props = {
    taskId: 'task-1',
    taskTitle: 'main',
    onError: vi.fn(),
    onLog: vi.fn(),
    onChanged: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
  render(<DiscordTaskSyncAction {...props} />);
  return props;
}

describe('DiscordTaskSyncAction', () => {
  it('syncs a Discord task and refreshes task state', async () => {
    const user = userEvent.setup();
    vi.mocked(api.DiscordTaskSync).mockResolvedValue({
      enabled: true,
      connected: true,
      uptimeSeconds: 1,
      sync: { running: false },
    });
    const props = renderAction();

    await user.click(screen.getByRole('button', { name: 'Sync with Discord' }));

    await waitFor(() => expect(api.DiscordTaskSync).toHaveBeenCalledWith('task-1'));
    expect(props.onError).toHaveBeenCalledWith('');
    expect(props.onLog).toHaveBeenCalledWith('$ sync Discord task "main"');
    expect(props.onLog).toHaveBeenCalledWith('[ok] sync Discord task "main"');
    expect(props.onChanged).toHaveBeenCalledTimes(1);
  });

  it('surfaces sync failures without refreshing task state', async () => {
    const user = userEvent.setup();
    vi.mocked(api.DiscordTaskSync).mockRejectedValue(new Error('sync timeout'));
    const props = renderAction();

    await user.click(screen.getByRole('button', { name: 'Sync with Discord' }));

    await waitFor(() => expect(props.onError).toHaveBeenCalledWith('sync timeout'));
    expect(props.onLog).toHaveBeenCalledWith('$ sync Discord task "main"');
    expect(props.onLog).toHaveBeenCalledWith('[error] sync Discord task "main": sync timeout');
    expect(props.onChanged).not.toHaveBeenCalled();
  });

  it('disables the button while sync is pending', async () => {
    const user = userEvent.setup();
    let resolveSync: () => void = () => {};
    vi.mocked(api.DiscordTaskSync).mockReturnValue(
      new Promise((resolve) => {
        resolveSync = () => resolve({ enabled: true, connected: true, uptimeSeconds: 1, sync: { running: false } });
      }),
    );
    renderAction();

    await user.click(screen.getByRole('button', { name: 'Sync with Discord' }));

    expect(screen.getByRole('button', { name: 'Syncing...' })).toBeDisabled();
    resolveSync();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Sync with Discord' })).not.toBeDisabled());
  });
});
