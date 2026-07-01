import { RefreshCw, Trash2 } from 'lucide-react';

import type { MonitorTask } from '../../api';
import {
  statusClass,
  statusLabel,
  taskPreviewDescription,
} from '../../appLogic';
import { AgentBadge, AllMightyBadge } from '../../components/badges';
import type { Project } from '../../types';
import { EmptyState, ErrorBar, Header, IconButton, type ThemeMode } from '../../ui';

type MonitorViewProps = {
  tasks: MonitorTask[];
  projects: Project[];
  error: string;
  refreshSeconds: number;
  busy: boolean;
  onRefresh: () => void;
  onDeleteTask: (task: MonitorTask) => void;
  onClearStaleTasks: (tasks: MonitorTask[]) => void;
  onOpenWorkspace: (projectID: string, taskID: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
  nowMs?: number;
};

const staleThresholdMs = 48 * 60 * 60 * 1000;

export function MonitorView({
  tasks,
  projects,
  error,
  refreshSeconds,
  busy,
  onRefresh,
  onDeleteTask,
  onClearStaleTasks,
  onOpenWorkspace,
  theme,
  onToggleTheme,
  nowMs = Date.now(),
}: MonitorViewProps) {
  const activeCount = tasks.filter((task) => task.status === 'active').length;
  const waitingCount = tasks.filter((task) => task.status === 'waiting').length;
  const completeCount = tasks.filter((task) => task.status === 'complete').length;
  const offlineCount = tasks.filter((task) => task.status === 'offline').length;
  const staleTasks = tasks.filter((task) => isStaleTask(task, nowMs));
  const projectCount = new Set(tasks.map((task) => task.projectId)).size;

  return (
    <main className="app-shell monitor-view">
      <Header title="Monitor" subtitle="Agent task status across registered workspaces" theme={theme} onToggleTheme={onToggleTheme}>
        {staleTasks.length > 0 && (
          <button className="danger-button monitor-clear-stale" disabled={busy} onClick={() => onClearStaleTasks(staleTasks)}>
            <Trash2 size={16} />
            Clear stale
          </button>
        )}
        <IconButton label="Refresh monitor" onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error} />
      <section className="monitor-summary">
        <MetricCard label="Tasks" value={tasks.length} detail={`Auto-refreshes every ${refreshSeconds}s`} />
        <MetricCard label="Active" value={activeCount} detail="Output is changing" />
        <MetricCard label="Waiting" value={waitingCount} detail="Session is idle" />
        <MetricCard label="Complete" value={completeCount} detail="Agent returned to shell" />
        <MetricCard label="Offline" value={offlineCount} detail={`${projectCount}/${projects.length} projects shown`} />
        <MetricCard label="Stale" value={staleTasks.length} detail="No recorded activity for 48h" />
      </section>
      {tasks.length === 0 ? (
        <EmptyState title="No tasks" detail="Create a task from Workspace to start monitoring agent state." />
      ) : (
        <section className="monitor-table">
          <div className="monitor-row monitor-head">
            <span>Task</span>
            <span>Project</span>
            <span>Status</span>
            <span>Agent</span>
            <span>Mode</span>
            <span>Lifetime</span>
            <span>Last activity</span>
            <span>Actions</span>
          </div>
          {tasks.map((task) => {
            const stale = isStaleTask(task, nowMs);
            return (
              <div className={`monitor-row ${stale ? 'monitor-row-stale' : ''}`} key={task.id}>
                <span>
                  <strong>{task.title}</strong>
                  {taskPreviewDescription(task) && <small>{taskPreviewDescription(task)}</small>}
                </span>
                <span>
                  <strong>{task.projectName}</strong>
                  <small>{task.projectPath}</small>
                </span>
                <span className="monitor-status-cell">
                  <span className={`status-pill ${statusClass(task.status)}`} title={`Status: ${task.status || 'unknown'}`}>
                    {statusLabel(task.status)}
                  </span>
                  {stale && <span className="status-pill stale" title="No task activity has been recorded for at least 48 hours.">Stale</span>}
                  {task.sessionName && <small>{task.sessionName}</small>}
                </span>
                <span><AgentBadge agent={task.agent} /></span>
                <span>{task.allMighty ? <AllMightyBadge /> : 'Standard'}</span>
                <span title={`Created ${formatDateTime(task.createdAt)}`}>{formatDuration(nowMs - Date.parse(task.createdAt))}</span>
                <span title={formatDateTime(task.updatedAt)}>{formatRelativeTime(task.updatedAt, nowMs)}</span>
                <span className="monitor-actions">
                  {stale && (
                    <button className="danger-button compact" disabled={busy} onClick={() => onDeleteTask(task)}>
                      Delete
                    </button>
                  )}
                  <button className="text-button" onClick={() => onOpenWorkspace(task.projectId, task.id)}>Open</button>
                </span>
              </div>
            );
          })}
        </section>
      )}
    </main>
  );
}

function MetricCard({ label, value, detail }: { label: string; value: number; detail: string }) {
  return (
    <article className="metric-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function isStaleTask(task: MonitorTask, nowMs: number): boolean {
  const updatedMs = Date.parse(task.updatedAt);
  return Number.isFinite(updatedMs) && nowMs - updatedMs >= staleThresholdMs;
}

function formatRelativeTime(value: string, nowMs: number): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return 'unknown';
  const elapsed = Math.max(0, nowMs - timestamp);
  if (elapsed < 60_000) return 'just now';
  return `${formatDuration(elapsed)} ago`;
}

function formatDuration(durationMs: number): string {
  if (!Number.isFinite(durationMs)) return 'unknown';
  const totalMinutes = Math.max(0, Math.floor(durationMs / 60_000));
  if (totalMinutes < 1) return '<1m';
  if (totalMinutes < 60) return `${totalMinutes}m`;
  const totalHours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  if (totalHours < 24) return minutes > 0 ? `${totalHours}h ${minutes}m` : `${totalHours}h`;
  const days = Math.floor(totalHours / 24);
  const hours = totalHours % 24;
  return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
}

function formatDateTime(value: string): string {
  const timestamp = Date.parse(value);
  if (!Number.isFinite(timestamp)) return 'unknown';
  return new Date(timestamp).toLocaleString([], {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  });
}
