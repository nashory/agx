import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  CircleHelp,
  Mic,
  Play,
  RefreshCw,
  ShieldCheck,
  Square,
  SquareTerminal,
} from 'lucide-react';

import {
  agentLabel,
  clampNumber,
  defaultPreferences,
  type UserPreferences,
} from '../../appLogic';
import type { Agent, RuntimeConfigInfo, RuntimeStatusInfo, VoiceSTTConfig } from '../../types';
import { Header, type ThemeMode } from '../../ui';

type RuntimeAction = () => Promise<RuntimeStatusInfo>;

type RuntimeStartupViewProps = {
  runtimeStatus: RuntimeStatusInfo;
  busy: boolean;
  onRefreshRuntime: () => Promise<void>;
  onStartRuntime: RuntimeAction;
  onInstallRuntimeService: RuntimeAction;
  theme: ThemeMode;
  onToggleTheme: () => void;
};

export function RuntimeStartupView({
  runtimeStatus,
  busy,
  onRefreshRuntime,
  onStartRuntime,
  onInstallRuntimeService,
  theme,
  onToggleTheme,
}: RuntimeStartupViewProps) {
  return (
    <main className="app-shell">
      <Header title="Runtime" subtitle="AGX daemon is required for projects, tasks, and Discord" theme={theme} onToggleTheme={onToggleTheme} />
      <section className="settings-grid runtime-startup-grid">
        <section className="settings-panel runtime-startup-panel">
          <h2>Daemon</h2>
          <div className="setting-row">
            <div>
              <SettingHeading label="Status" help="Whether the AGX runtime daemon is reachable from Desktop." />
              <span>{runtimeStatus.error || 'Not running'}</span>
            </div>
            <span className="status-pill error" title="Desktop cannot reach the AGX runtime daemon.">
              Stopped
            </span>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Controls" help="Runtime actions. Start runs the daemon for this session; Install registers the platform service." />
              <span>Start the runtime for this session or install the platform service.</span>
            </div>
            <div className="runtime-control-buttons">
              <button className="text-button" disabled={busy} onClick={() => void onRefreshRuntime()}>
                <RefreshCw size={15} />
                Retry
              </button>
              <button className="text-button" disabled={busy} onClick={() => void onStartRuntime()}>
                <Play size={15} />
                Start
              </button>
              <button className="text-button" disabled={busy} onClick={() => void onInstallRuntimeService()}>
                <ShieldCheck size={15} />
                Install
              </button>
            </div>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Transport" help="Local transport used by Desktop, CLI, and integrations to talk to the runtime daemon." />
              <span>{runtimeTransportDetail(runtimeStatus)}</span>
            </div>
            <SquareTerminal size={18} aria-hidden="true" />
          </div>
        </section>
      </section>
    </main>
  );
}

type SettingsViewProps = {
  preferences: UserPreferences;
  onPreferencesChange: (preferences: UserPreferences) => void;
  theme: ThemeMode;
  onThemeChange: (theme: ThemeMode) => void;
  onToggleTheme: () => void;
  onResetDatabase: () => Promise<void>;
  runtimeStatus: RuntimeStatusInfo;
  runtimeConfig: RuntimeConfigInfo;
  agents: Agent[];
  onDefaultAgentChange: (agentName: string) => Promise<void>;
  onVoiceSTTChange: (voiceStt: VoiceSTTConfig) => Promise<void>;
  onVoiceSTTSetup: () => Promise<void>;
  onRefreshRuntime: () => Promise<void>;
  onStartRuntime: RuntimeAction;
  onInstallRuntimeService: RuntimeAction;
  onStopRuntime: RuntimeAction;
  busy: boolean;
};

export function SettingsView({
  preferences,
  onPreferencesChange,
  theme,
  onThemeChange,
  onToggleTheme,
  onResetDatabase,
  runtimeStatus,
  runtimeConfig,
  agents,
  onDefaultAgentChange,
  onVoiceSTTChange,
  onVoiceSTTSetup,
  onRefreshRuntime,
  onStartRuntime,
  onInstallRuntimeService,
  onStopRuntime,
  busy,
}: SettingsViewProps) {
  const [confirmingReset, setConfirmingReset] = useState(false);
  const [savingDefaultAgent, setSavingDefaultAgent] = useState(false);
  const [savingVoiceSTT, setSavingVoiceSTT] = useState(false);
  const [localVoiceSTT, setLocalVoiceSTT] = useState<VoiceSTTConfig>(() => runtimeConfig.voiceStt ?? defaultVoiceSTTConfig());
  const defaultAgentName = runtimeConfig.defaultAgent || 'codex';
  const defaultAgentOptions = agents.some((agent) => agent.name === defaultAgentName)
    ? agents
    : [{ name: defaultAgentName, command: defaultAgentName, description: '', available: false }, ...agents];

  function update<K extends keyof UserPreferences>(key: K, value: UserPreferences[K]) {
    onPreferencesChange({ ...preferences, [key]: value });
  }

  useEffect(() => {
    setLocalVoiceSTT(runtimeConfig.voiceStt ?? defaultVoiceSTTConfig());
  }, [runtimeConfig.voiceStt]);

  function updateVoiceSTT<K extends keyof VoiceSTTConfig>(key: K, value: VoiceSTTConfig[K]) {
    setLocalVoiceSTT((current) => ({ ...current, [key]: value }));
  }

  async function saveDefaultAgent(agentName: string) {
    setSavingDefaultAgent(true);
    try {
      await onDefaultAgentChange(agentName);
    } finally {
      setSavingDefaultAgent(false);
    }
  }

  async function saveVoiceSTT() {
    setSavingVoiceSTT(true);
    try {
      await onVoiceSTTChange(localVoiceSTT);
    } finally {
      setSavingVoiceSTT(false);
    }
  }

  async function setupVoiceSTT() {
    setSavingVoiceSTT(true);
    try {
      await onVoiceSTTSetup();
    } finally {
      setSavingVoiceSTT(false);
    }
  }

  async function resetDatabase() {
    await onResetDatabase();
    setConfirmingReset(false);
  }

  return (
    <main className="app-shell">
      <Header title="Settings" subtitle="Desktop preferences for project onboarding and monitoring" theme={theme} onToggleTheme={onToggleTheme} />
      <section className="settings-grid">
        <section className="settings-panel runtime-settings-panel">
          <h2>Runtime</h2>
          <div className="setting-row">
            <div>
              <SettingHeading label="Daemon" help="The background AGX runtime process that owns sessions, Discord bridge state, sockets, and recovery." />
              <span>{runtimeStatus.running ? runtimeDetail(runtimeStatus) : runtimeStatus.error || 'Not running'}</span>
            </div>
            <button className="text-button" onClick={() => void onRefreshRuntime()}>
              <RefreshCw size={15} />
              Refresh
            </button>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Controls" help="Start launches the daemon now, Install registers the platform service, and Stop terminates the current daemon." />
              <span>Start the runtime now, install the platform service, or stop the running daemon.</span>
            </div>
            <div className="runtime-control-buttons">
              <button className="text-button" disabled={busy || runtimeStatus.running} onClick={() => void onStartRuntime()}>
                <Play size={15} />
                Start
              </button>
              <button className="text-button" disabled={busy} onClick={() => void onInstallRuntimeService()}>
                <ShieldCheck size={15} />
                Install
              </button>
              <button className="text-button" disabled={busy || !runtimeStatus.running} onClick={() => void onStopRuntime()}>
                <Square size={15} />
                Stop
              </button>
            </div>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Transport" help="Local transport used by Desktop and the CLI to send requests to the runtime daemon." />
              <span>{runtimeTransportDetail(runtimeStatus)}</span>
            </div>
            <span className={`status-pill ${runtimeStatus.running ? 'ok' : 'error'}`} title={runtimeStatus.running ? 'Runtime transport is reachable.' : 'Runtime transport is not reachable.'}>
              {runtimeStatus.running ? 'Running' : 'Stopped'}
            </span>
          </div>
          <div className="setting-row">
            <div>
              <SettingHeading label="Recovery" help="Startup cleanup summary for stale task sessions and orphan runtime-owned worktrees." />
              <span>{runtimeRecoveryDetail(runtimeStatus)}</span>
            </div>
            <span className="status-pill neutral" title="Recovery only removes sessions and worktrees that AGX runtime owns.">
              Runtime-owned
            </span>
          </div>
        </section>
        <section className="settings-panel">
          <h2>Appearance</h2>
          <div className="setting-row">
            <div>
              <strong>Theme</strong>
              <span>Controls the desktop color scheme.</span>
            </div>
            <select value={theme} onChange={(event) => onThemeChange(event.target.value === 'light' ? 'light' : 'dark')}>
              <option value="dark">Dark</option>
              <option value="light">Light</option>
            </select>
          </div>
          <label className="setting-row checkbox-row">
            <div>
              <strong>Action log</strong>
              <span>Show the compact command/activity log at the bottom edge.</span>
            </div>
            <input
              type="checkbox"
              checked={preferences.showActionLog}
              onChange={(event) => update('showActionLog', event.target.checked)}
            />
          </label>
        </section>
        <section className="settings-panel">
          <h2>Workspace</h2>
          <div className="setting-row">
            <div>
              <strong>Default agent</strong>
              <span>Used when a task or project does not choose a specific agent.</span>
            </div>
            <select value={defaultAgentName} disabled={busy || savingDefaultAgent} onChange={(event) => void saveDefaultAgent(event.target.value)}>
              {defaultAgentOptions.map((agent) => (
                <option key={agent.name} value={agent.name}>
                  {agentLabel(agent.name)}{agent.available ? '' : ' (not installed)'}
                </option>
              ))}
            </select>
          </div>
          <div className="setting-row">
            <div>
              <strong>Default task view</strong>
              <span>Choose the task layout used for newly opened projects.</span>
            </div>
            <select value={preferences.defaultTaskView} onChange={(event) => update('defaultTaskView', event.target.value === 'list' ? 'list' : 'grid')}>
              <option value="grid">Grid</option>
              <option value="list">List</option>
            </select>
          </div>
          <label className="setting-row checkbox-row">
            <div>
              <strong>Open after adding project</strong>
              <span>Jump into a project immediately after it is registered.</span>
            </div>
            <input
              type="checkbox"
              checked={preferences.openProjectAfterAdd}
              onChange={(event) => update('openProjectAfterAdd', event.target.checked)}
            />
          </label>
          <label className="setting-row checkbox-row">
            <div>
              <strong>Default all-mighty mode</strong>
              <span>Preselect unrestricted agent permissions when creating a task.</span>
            </div>
            <input
              type="checkbox"
              checked={preferences.defaultAllMighty}
              onChange={(event) => update('defaultAllMighty', event.target.checked)}
            />
          </label>
          <div className="setting-row">
            <div>
              <strong>Repository suggestions</strong>
              <span>Maximum number of Git repositories shown in the add-project modal.</span>
            </div>
            <input
              type="number"
              min={6}
              max={50}
              value={preferences.projectCandidateLimit}
              onChange={(event) => update('projectCandidateLimit', clampNumber(event.target.value, 6, 50, defaultPreferences.projectCandidateLimit))}
            />
          </div>
        </section>
        <section className="settings-panel">
          <h2>Voice Transcription</h2>
          <div className="setting-row">
            <div>
              <SettingHeading label="Mode" help="Local Whisper is optional. Auto uses it when local ffmpeg, Whisper, and a model can be resolved automatically or from saved settings." />
              <span>{voiceSTTStatus(localVoiceSTT)}</span>
            </div>
            <select
              value={localVoiceSTT.mode}
              disabled={busy || savingVoiceSTT}
              onChange={(event) => updateVoiceSTT('mode', voiceSTTMode(event.target.value))}
            >
              <option value="disabled">Disabled</option>
              <option value="auto">Auto</option>
              <option value="enabled">Enabled</option>
            </select>
          </div>
          <div className="setting-row">
            <div>
              <strong>ffmpeg</strong>
              <span>Command or absolute path used to convert Discord Ogg voice messages.</span>
            </div>
            <input
              type="text"
              value={localVoiceSTT.ffmpegPath}
              disabled={busy || savingVoiceSTT}
              placeholder="ffmpeg"
              onChange={(event) => updateVoiceSTT('ffmpegPath', event.target.value)}
            />
          </div>
          <div className="setting-row">
            <div>
              <strong>Whisper binary</strong>
              <span>Command or absolute path for local whisper.cpp transcription.</span>
            </div>
            <input
              type="text"
              value={localVoiceSTT.whisperPath}
              disabled={busy || savingVoiceSTT}
              placeholder="whisper-cli"
              onChange={(event) => updateVoiceSTT('whisperPath', event.target.value)}
            />
          </div>
          <div className="setting-row">
            <div>
              <strong>Model path</strong>
              <span>Local Whisper model file. Setup stores the default model under the AGX config directory.</span>
            </div>
            <input
              type="text"
              value={localVoiceSTT.modelPath}
              disabled={busy || savingVoiceSTT}
              placeholder="Auto"
              onChange={(event) => updateVoiceSTT('modelPath', event.target.value)}
            />
          </div>
          <div className="setting-row">
            <div>
              <strong>Language</strong>
              <span>Use auto unless you want to bias transcription toward a language.</span>
            </div>
            <select value={localVoiceSTT.language || 'auto'} disabled={busy || savingVoiceSTT} onChange={(event) => updateVoiceSTT('language', event.target.value)}>
              <option value="auto">Auto</option>
              <option value="ko">Korean</option>
              <option value="en">English</option>
              <option value="ja">Japanese</option>
              <option value="zh">Chinese</option>
            </select>
          </div>
          <div className="setting-row">
            <div>
              <strong>Timeout</strong>
              <span>Maximum time to spend on one local voice transcription.</span>
            </div>
            <input
              type="text"
              value={localVoiceSTT.timeout}
              disabled={busy || savingVoiceSTT}
              placeholder="60s"
              onChange={(event) => updateVoiceSTT('timeout', event.target.value)}
            />
          </div>
          <div className="setting-row">
            <div>
              <strong>Local only</strong>
              <span>Audio is stored locally and never sent to a cloud STT service by AGX.</span>
            </div>
            <div className="runtime-control-buttons">
              <button className="text-button" disabled={busy || savingVoiceSTT} onClick={() => void setupVoiceSTT()}>
                <RefreshCw size={15} />
                Setup
              </button>
              <button className="text-button" disabled={busy || savingVoiceSTT} onClick={() => void saveVoiceSTT()}>
                <Mic size={15} />
                Save
              </button>
            </div>
          </div>
        </section>
        <section className="settings-panel">
          <h2>Monitor</h2>
          <div className="setting-row">
            <div>
              <strong>Refresh interval</strong>
              <span>How often Monitor refreshes live agent status.</span>
            </div>
            <input
              type="number"
              min={2}
              max={60}
              value={preferences.monitorRefreshSeconds}
              onChange={(event) => update('monitorRefreshSeconds', clampNumber(event.target.value, 2, 60, defaultPreferences.monitorRefreshSeconds))}
            />
          </div>
        </section>
        <section className="settings-panel danger-zone">
          <h2>Danger Zone</h2>
          <div className="setting-row">
            <div>
              <strong>Reset AGX database</strong>
              <span>Delete all registered projects, tasks, runtime state, log streams, and AGX tmux sessions.</span>
            </div>
            <button className="danger-button" disabled={busy} onClick={() => setConfirmingReset(true)}>
              Reset Database
            </button>
          </div>
        </section>
      </section>
      {confirmingReset && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingReset(false)}>
          <section className="confirm-modal danger-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Reset AGX Database</h2>
            <p>This deletes every AGX project and task from the local database, stops AGX tmux sessions, and clears task log streams. Repository files are not deleted.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingReset(false)}>Cancel</button>
              <button className="danger-button" disabled={busy} onClick={resetDatabase}>Reset Everything</button>
            </div>
          </section>
        </div>
      ), document.body)}
    </main>
  );
}

function defaultVoiceSTTConfig(): VoiceSTTConfig {
  return {
    mode: 'auto',
    ffmpegPath: '',
    whisperPath: '',
    modelPath: '',
    language: 'auto',
    timeout: '60s',
  };
}

function voiceSTTMode(value: string): VoiceSTTConfig['mode'] {
  return value === 'disabled' || value === 'enabled' ? value : 'auto';
}

function voiceSTTStatus(config: VoiceSTTConfig): string {
  if (config.mode === 'disabled') return 'Voice messages are saved, but not transcribed.';
  const missing = [];
  if (!config.ffmpegPath.trim()) missing.push('ffmpeg via PATH');
  if (!config.whisperPath.trim()) missing.push('Whisper via PATH');
  if (!config.modelPath.trim()) missing.push('model via Setup');
  if (missing.length > 0) return `Local STT ${config.mode}; will auto-resolve ${missing.join(', ')}.`;
  return `Local STT ${config.mode}; ready to transcribe voice messages.`;
}

function runtimeDetail(status: RuntimeStatusInfo): string {
  const parts = [`pid ${status.pid ?? 'unknown'}`];
  if (status.version) parts.push(status.version);
  if (status.uptimeSeconds > 0) parts.push(`up ${formatRuntimeUptime(status.uptimeSeconds)}`);
  return parts.join(' · ');
}

function runtimeRecoveryDetail(status: RuntimeStatusInfo): string {
  const recovery = status.recovery ?? {};
  const offline = recovery.offline ?? 0;
  const cleared = recovery.cleared ?? 0;
  const orphans = recovery.orphans ?? 0;
  return `${offline} offline, ${cleared} stale sessions cleared, ${orphans} orphan worktrees removed`;
}

function runtimeTransportDetail(status: RuntimeStatusInfo): string {
  return status.transport || status.socketPath || 'Unavailable';
}

function formatRuntimeUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  const remainingMinutes = minutes % 60;
  return remainingMinutes > 0 ? `${hours}h ${remainingMinutes}m` : `${hours}h`;
}

function HelpBadge({ text }: { text: string }) {
  return (
    <span className="help-badge" data-tooltip={text} tabIndex={0} aria-label={text}>
      <CircleHelp size={13} />
    </span>
  );
}

function SettingHeading({ label, help }: { label: string; help: string }) {
  return (
    <span className="setting-heading">
      <strong>{label}</strong>
      <HelpBadge text={help} />
    </span>
  );
}
