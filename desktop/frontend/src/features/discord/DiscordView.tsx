import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { ExternalLink, LockKeyhole, RefreshCw } from 'lucide-react';
import { api } from '../../api';
import type { DiscordStatusInfo } from '../../types';
import { Header, IconButton, type ThemeMode } from '../../ui';
import { errorMessage, timestamp } from '../../appLogic';

export function DiscordView({
  status,
  statusLoading,
  onStatus,
  onRefresh,
  onLog,
  onError,
  theme,
  onToggleTheme,
}: {
  status: DiscordStatusInfo;
  statusLoading: boolean;
  onStatus: (status: DiscordStatusInfo) => void;
  onRefresh: () => Promise<void>;
  onLog: (message: string) => void;
  onError: (message: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
}) {
  const [token, setToken] = useState('');
  const [guildID, setGuildID] = useState(status.guildId ?? '');
  const [allowedUserID, setAllowedUserID] = useState('');
  const [events, setEvents] = useState<string[]>([]);
  const [busyAction, setBusyAction] = useState<'connect' | 'disconnect' | 'sync' | 'reset' | 'invite' | null>(null);
  const [confirmingReset, setConfirmingReset] = useState(false);
  const busy = busyAction !== null;
  const discordRunningElsewhere = status.error?.includes('discord bridge already running') ?? false;
  const checkingConnection = statusLoading || (status.enabled && !status.connected && !status.error && !discordRunningElsewhere);
  const hasStoredToken = Boolean(status.maskedBotToken);
  const shouldShowDisconnect = status.connected || discordRunningElsewhere;
  const hardSyncRunning = status.sync?.running && status.sync.kind === 'hard';
  const statusLabel = checkingConnection ? 'checking' : status.connected ? 'connected' : discordRunningElsewhere ? 'already running' : status.enabled ? 'disconnected' : 'disabled';
  const statusTone = status.connected ? 'active' : checkingConnection || status.enabled || discordRunningElsewhere ? 'waiting' : 'offline';
  const statusDetail = checkingConnection
    ? 'Checking Discord connection...'
    : status.error || (status.connected ? `Connected${status.guildName ? ` to ${status.guildName}` : ''}` : status.enabled ? 'Enabled but disconnected' : 'Not configured');
  const canReuseStoredToken = hasStoredToken && status.enabled;
  const missingRequiredToken = token.trim() === '' && !canReuseStoredToken;
  const tokenLocked = hasStoredToken && (checkingConnection || shouldShowDisconnect) && token.trim() === '';
  const connectionLocked = busy || checkingConnection;
  const syncLocked = connectionLocked || hardSyncRunning;

  useEffect(() => {
    setGuildID(status.guildId ?? '');
    setAllowedUserID((status.allowedUserIds ?? [])[0] ?? '');
  }, [status.guildId, status.allowedUserIds]);

  useEffect(() => {
    const detail = checkingConnection ? 'Checking connection' : status.error || (status.connected ? 'Connected' : status.enabled ? 'Disconnected' : 'Disabled');
    setEvents((value) => [`${timestamp()} ${detail}`, ...value].slice(0, 20));
  }, [checkingConnection, status.connected, status.enabled, status.error]);

  useEffect(() => {
    if (!status.sync?.stage) return;
    const detail = status.sync.error ? `${status.sync.stage}: ${status.sync.error}` : status.sync.stage;
    setEvents((value) => [`${timestamp()} ${detail}`, ...value].slice(0, 20));
  }, [status.sync?.stage, status.sync?.error]);

  async function connect() {
    if (missingRequiredToken) {
      onError('Discord bot token is required');
      setEvents((value) => [`${timestamp()} Bot token is required`, ...value].slice(0, 20));
      return;
    }
    if (!guildID.trim()) {
      onError('Discord server ID is required');
      return;
    }
    if (!allowedUserID.trim()) {
      onError('Allowed Discord user ID is required');
      return;
    }
    setBusyAction('connect');
    onError('');
    onLog('$ discord connect');
    try {
      const next = await api.DiscordConnect(token, guildID, allowedUserID);
      onStatus(next);
      setToken('');
      setGuildID(next.guildId ?? '');
      setAllowedUserID((next.allowedUserIds ?? [])[0] ?? '');
      setEvents((value) => [`${timestamp()} Connect requested`, ...value].slice(0, 20));
      onLog('[ok] discord connect');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord connect: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  async function disconnect() {
    setBusyAction('disconnect');
    onError('');
    onLog('$ discord disconnect');
    try {
      onStatus(await api.DiscordDisconnect());
      setEvents((value) => [`${timestamp()} Disconnect requested`, ...value].slice(0, 20));
      onLog('[ok] discord disconnect');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord disconnect: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  async function syncNow() {
    setBusyAction('sync');
    onError('');
    onLog('$ discord soft sync');
    try {
      const next = await api.DiscordSoftSync();
      onStatus(next);
      setEvents((value) => [`${timestamp()} Soft sync completed`, ...value].slice(0, 20));
      onLog('[ok] discord soft sync');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord soft sync: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  async function inviteBot() {
    setBusyAction('invite');
    onError('');
    onLog('$ discord invite bot');
    try {
      await api.OpenDiscordInvite(token);
      setEvents((value) => [`${timestamp()} Invite opened in browser`, ...value].slice(0, 20));
      onLog('[ok] discord invite bot');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord invite bot: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  function requestDiscordReset() {
    onLog('$ discord hard sync prompt');
    setConfirmingReset(true);
  }

  async function resetDiscordServer() {
    setConfirmingReset(false);
    setBusyAction('reset');
    onError('');
    onLog('$ discord hard sync');
    try {
      const next = await api.DiscordHardSync();
      onStatus(next);
      setEvents((value) => [`${timestamp()} ${next.sync?.stage ?? 'Hard sync started'}`, ...value].slice(0, 20));
      onLog(next.sync?.running ? '[ok] discord hard sync started' : '[ok] discord hard sync');
    } catch (err) {
      const message = errorMessage(err);
      onError(message);
      onLog(`[error] discord hard sync: ${message}`);
    } finally {
      setBusyAction(null);
    }
  }

  return (
    <main className="app-shell">
      <Header title="Discord" subtitle="Remote AGX control from Discord" theme={theme} onToggleTheme={onToggleTheme}>
        <IconButton label="Refresh Discord status" disabled={statusLoading} onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <section className="settings-grid discord-grid">
        <section className="settings-panel discord-connection-panel">
          <h2>Connection</h2>
          {status.error && (
            <div className="discord-error">
              <strong>{discordRunningElsewhere ? 'Connection already active' : 'Connection failed'}</strong>
              <span>{status.error}</span>
            </div>
          )}
          <div className="setting-row">
            <div>
              <strong>Status</strong>
              <span>{statusDetail}</span>
            </div>
            <span className={`status-pill ${statusTone}`}>
              {checkingConnection && <span className="button-spinner" aria-hidden="true" />}
              {statusLabel}
            </span>
          </div>
          <div className="setting-row">
            <div>
              <strong>Server ID</strong>
              <span>{status.guildName ? `Connected server: ${status.guildName}` : 'Saved locally after you connect; kept after disconnect for convenience.'}</span>
            </div>
            <input value={guildID} disabled={connectionLocked} onChange={(event) => setGuildID(event.target.value)} placeholder="1234567890123456789" />
          </div>
          <div className="setting-row">
            <div>
              <strong>Bot token</strong>
              <span>{canReuseStoredToken ? 'Stored token is locked.' : 'Required to connect; disconnect clears the stored token.'}</span>
            </div>
            {tokenLocked ? (
              <div className="locked-token-field" title="Stored bot token">
                <LockKeyhole size={15} aria-hidden="true" />
                <span>{status.maskedBotToken}</span>
              </div>
            ) : (
              <input value={token} type="password" disabled={connectionLocked} onChange={(event) => setToken(event.target.value)} placeholder={canReuseStoredToken ? 'Leave blank to retry with stored token' : 'Discord bot token'} />
            )}
          </div>
          <div className="setting-row">
            <div>
              <strong>Allowed user ID</strong>
              <span>Single Discord user allowed to control AGX; saved locally after connect.</span>
            </div>
            <input value={allowedUserID} disabled={connectionLocked} onChange={(event) => setAllowedUserID(event.target.value)} placeholder="123456789012345678" />
          </div>
          <div className="setting-actions">
            <button className="text-button" disabled={connectionLocked || missingRequiredToken} onClick={inviteBot} aria-busy={busyAction === 'invite'}>
              {busyAction === 'invite' ? <span className="button-spinner" aria-hidden="true" /> : <ExternalLink size={16} aria-hidden="true" />}
              {busyAction === 'invite' ? 'Opening...' : 'Invite AGX Coding'}
            </button>
            {shouldShowDisconnect ? (
              <button className="text-button" disabled={connectionLocked} onClick={disconnect}>
                {busyAction === 'disconnect' && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'disconnect' ? 'Disconnecting...' : 'Disconnect'}
              </button>
            ) : (
              <button className="primary-button" disabled={connectionLocked || missingRequiredToken || !guildID.trim() || !allowedUserID.trim()} onClick={connect} aria-busy={busyAction === 'connect'}>
                {busyAction === 'connect' && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'connect' ? 'Connecting...' : 'Connect'}
              </button>
            )}
          </div>
        </section>
        <section className="settings-panel">
          <div className="panel-heading">
            <h2>Sync Status</h2>
            <div className="panel-actions">
              <button className="text-button" disabled={syncLocked || !status.connected} onClick={syncNow}>
                {busyAction === 'sync' && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'sync' ? 'Soft syncing...' : 'Soft Sync'}
              </button>
              <button className="danger-button" disabled={connectionLocked || hardSyncRunning || !status.connected} onClick={requestDiscordReset}>
                {(busyAction === 'reset' || hardSyncRunning) && <span className="button-spinner" aria-hidden="true" />}
                {busyAction === 'reset' ? 'Starting...' : hardSyncRunning ? 'Hard syncing...' : 'Hard Sync'}
              </button>
            </div>
          </div>
          <div className="discord-sync-list">
            {status.sync?.stage && (
              <div>
                <strong>Hard Sync</strong>
                <span>{status.sync.stage}{status.sync.error ? `: ${status.sync.error}` : ''}</span>
              </div>
            )}
            <div>
              <strong>#agx-control</strong>
              <span>{checkingConnection ? 'Checking connection' : status.connected ? 'Created or verified during sync' : 'Connect to verify'}</span>
            </div>
            <div>
              <strong>Projects</strong>
              <span>{checkingConnection ? 'Waiting for Discord' : status.connected ? 'Categories mirror registered projects' : 'Waiting for connection'}</span>
            </div>
            <div>
              <strong>Active tasks</strong>
              <span>{checkingConnection ? 'Waiting for Discord' : status.connected ? 'Channels mirror active and waiting tasks' : 'Waiting for connection'}</span>
            </div>
          </div>
        </section>
        <section className="settings-panel">
          <h2>Event Log</h2>
          <div className="discord-event-log">
            {events.length === 0 ? (
              <span>No Discord events yet.</span>
            ) : events.map((event, index) => (
              <span key={`${event}-${index}`}>{event}</span>
            ))}
          </div>
        </section>
      </section>
      {confirmingReset && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingReset(false)}>
          <section className="confirm-modal danger-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Hard Sync Discord</h2>
            <p>This deletes AGX-managed Discord channels, categories, and message history, clears AGX Discord mappings, and recreates the Discord server view from the current AGX projects and tasks.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingReset(false)}>Cancel</button>
              <button className="danger-button" disabled={connectionLocked || hardSyncRunning} onClick={resetDiscordServer}>Hard Sync</button>
            </div>
          </section>
        </div>
      ), document.body)}
    </main>
  );
}
