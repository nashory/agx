import { RefreshCw } from 'lucide-react';

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
  onRefresh: () => void;
  onOpenWorkspace: (projectID: string, taskID: string) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
};

export function MonitorView({
  tasks,
  projects,
  error,
  refreshSeconds,
  onRefresh,
  onOpenWorkspace,
  theme,
  onToggleTheme,
}: MonitorViewProps) {
  const activeCount = tasks.filter((task) => task.status === 'active').length;
  const waitingCount = tasks.filter((task) => task.status === 'waiting').length;
  const completeCount = tasks.filter((task) => task.status === 'complete').length;
  const offlineCount = tasks.filter((task) => task.status === 'offline').length;
  const projectCount = new Set(tasks.map((task) => task.projectId)).size;

  return (
    <main className="app-shell monitor-view">
      <Header title="Monitor" subtitle="Agent task status across registered workspaces" theme={theme} onToggleTheme={onToggleTheme}>
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
            <span>Updated</span>
            <span />
          </div>
          {tasks.map((task) => (
            <div className="monitor-row" key={task.id}>
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
                {task.sessionName && <small>{task.sessionName}</small>}
              </span>
              <span><AgentBadge agent={task.agent} /></span>
              <span>{task.allMighty ? <AllMightyBadge /> : 'Standard'}</span>
              <span>{new Date(task.updatedAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}</span>
              <button className="text-button" onClick={() => onOpenWorkspace(task.projectId, task.id)}>Open</button>
            </div>
          ))}
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
