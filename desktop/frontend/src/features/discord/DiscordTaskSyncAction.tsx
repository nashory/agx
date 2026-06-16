import { useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { api } from '../../api';
import { errorMessage } from '../../appLogic';

export function DiscordTaskSyncAction({
  taskId,
  taskTitle,
  onError,
  onLog,
  onChanged,
}: {
  taskId: string;
  taskTitle: string;
  onError: (message: string) => void;
  onLog: (message: string) => void;
  onChanged: () => Promise<void> | void;
}) {
  const [syncing, setSyncing] = useState(false);

  async function syncWithDiscord() {
    if (syncing) return;
    setSyncing(true);
    onError('');
    onLog(`$ sync Discord task "${taskTitle}"`);
    try {
      await api.DiscordTaskSync(taskId);
      onLog(`[ok] sync Discord task "${taskTitle}"`);
      await onChanged();
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] sync Discord task "${taskTitle}": ${message}`);
    } finally {
      setSyncing(false);
    }
  }

  return (
    <button className="text-button" disabled={syncing} onClick={() => void syncWithDiscord()}>
      <RefreshCw size={15} />
      {syncing ? 'Syncing...' : 'Sync with Discord'}
    </button>
  );
}
