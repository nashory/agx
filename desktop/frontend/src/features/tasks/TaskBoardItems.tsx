import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { Play, Square, Trash2 } from 'lucide-react';
import { api } from '../../api';
import { AgentBadge, AllMightyBadge, DiscordBadge, WorkspaceBadge } from '../../components/badges';
import type { Task } from '../../types';
import { IconButton } from '../../ui';
import {
  isDiscordTask,
  relativeTime,
  statusLabel,
  type DesktopAction,
} from '../../appLogic';

export function TaskCard({ task, busy, focused, onFocus, onOpen, onAction, index = 0 }: { task: Task; busy: boolean; focused: boolean; onFocus: () => void; onOpen: () => void; onAction: DesktopAction; index?: number }) {
  return (
    <article
      className={`task-card ${focused ? 'focused' : ''}`}
      style={{ animationDelay: `${Math.min(index * 30, 240)}ms` }}
      onClick={onFocus}
    >
      <button className="task-open" onClick={onOpen}>
        <span className="card-title">{task.title}</span>
        <span className="task-badge-row">
          {isDiscordTask(task) && <DiscordBadge />}
          <WorkspaceBadge mode={task.workspaceMode} />
          {task.allMighty && <AllMightyBadge />}
          <AgentBadge agent={task.agent} />
        </span>
        <span className="task-activity">Last activity {relativeTime(task.updatedAt)}</span>
        {task.lastUserPrompt && <span className="task-last-prompt">{task.lastUserPrompt}</span>}
      </button>
      <span className={`status-pill task-status-pin ${task.status}`}>{statusLabel(task.status)}</span>
      <TaskActions task={task} busy={busy} onAction={onAction} />
    </article>
  );
}

export function TaskList({ tasks, busy, focusedTaskID, onFocusTask, onSelectTask, onAction }: { tasks: Task[]; busy: boolean; focusedTaskID: string | null; onFocusTask: (taskID: string) => void; onSelectTask: (task: Task) => void; onAction: DesktopAction }) {
  return (
    <section className="task-table">
      {tasks.map((task) => (
        <div
          className={`task-row ${task.id === focusedTaskID ? 'focused' : ''}`}
          key={task.id}
          onClick={() => onFocusTask(task.id)}
        >
          <button onClick={() => onSelectTask(task)}>{task.title}</button>
          <span>{statusLabel(task.status)}</span>
          <span><AgentBadge agent={task.agent} /></span>
          <span className="task-list-badges">{isDiscordTask(task) && <DiscordBadge />}<WorkspaceBadge mode={task.workspaceMode} />{task.allMighty ? <AllMightyBadge /> : 'Standard'}</span>
          <span>{relativeTime(task.updatedAt)}</span>
          <TaskActions task={task} busy={busy} onAction={onAction} />
        </div>
      ))}
    </section>
  );
}

function TaskActions({ task, busy, onAction }: { task: Task; busy: boolean; onAction: DesktopAction }) {
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(task.title);

  useEffect(() => {
    if (!editing) setTitle(task.title);
  }, [editing, task.title]);

  function deleteTask() {
    setConfirmingDelete(false);
    onAction(() => api.DeleteTask(task.id), `delete task "${task.title}"`);
  }

  function updateTitle() {
    const nextTitle = title.trim();
    if (!nextTitle || nextTitle === task.title) {
      setEditing(false);
      return;
    }
    setEditing(false);
    onAction(async () => {
      await api.UpdateTaskTitle(task.id, nextTitle);
    }, `rename task "${task.title}" to "${nextTitle}"`);
  }

  return (
    <>
      <div className="icon-row">
        {!isDiscordTask(task) && task.status === 'offline' && (
          <IconButton label="Restart task" disabled={busy} onClick={() => onAction(async () => {
            await api.RunTask(task.id);
            return { taskID: task.id, expectSession: true };
          }, `restart task "${task.title}"`)}>
            <Play size={16} />
          </IconButton>
        )}
        {!isDiscordTask(task) && (task.status === 'active' || task.status === 'waiting' || task.status === 'complete') && (
          <IconButton label="Stop task" disabled={busy} onClick={() => onAction(() => api.StopTask(task.id), `stop task "${task.title}"`)}>
            <Square size={16} />
          </IconButton>
        )}
        <button className="text-button" disabled={busy} onClick={() => setEditing(true)}>
          Edit
        </button>
        <IconButton label="Delete task" disabled={busy} onClick={() => setConfirmingDelete(true)}>
          <Trash2 size={16} />
        </IconButton>
      </div>
      {editing && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setEditing(false)}>
          <section className="confirm-modal task-edit-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Edit Task</h2>
            <label>
              Task name
              <input
                value={title}
                onChange={(event) => setTitle(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') {
                    event.preventDefault();
                    updateTitle();
                  }
                  if (event.key === 'Escape') {
                    event.preventDefault();
                    setEditing(false);
                  }
                }}
                autoFocus
              />
            </label>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setEditing(false)}>Cancel</button>
              <button className="primary-button" disabled={busy || !title.trim()} onClick={updateTitle}>Save</button>
            </div>
          </section>
        </div>
      ), document.body)}
      {confirmingDelete && createPortal((
        <div className="modal-backdrop blurred" onMouseDown={() => setConfirmingDelete(false)}>
          <section className="confirm-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Delete Task</h2>
            <p>Delete "{task.title}" from AGX? This stops its session and removes its task worktree when AGX can do that safely.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setConfirmingDelete(false)}>Cancel</button>
              <button className="danger-button" disabled={busy} onClick={deleteTask}>Delete</button>
            </div>
          </section>
        </div>
      ), document.body)}
    </>
  );
}
