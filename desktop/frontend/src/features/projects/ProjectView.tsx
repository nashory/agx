import { useEffect, useRef, useState } from 'react';
import {
  CheckCircle2,
  Code2,
  Folder,
  FolderOpen,
  RefreshCw,
  Search,
  X,
} from 'lucide-react';

import { api } from '../../api';
import { errorMessage, isTextEntry, projectGridColumns } from '../../appLogic';
import type { LanguageStat, Project, ProjectCandidate } from '../../types';
import { EmptyState, ErrorBar, Header, IconButton, type ThemeMode } from '../../ui';

type ProjectViewProps = {
  projects: Project[];
  error: string;
  candidateLimit: number;
  openProjectAfterAdd: boolean;
  onRefresh: () => void;
  onOpenProject: (project: Project) => void;
  theme: ThemeMode;
  onToggleTheme: () => void;
};

export function ProjectView({
  projects,
  error,
  candidateLimit,
  openProjectAfterAdd,
  onRefresh,
  onOpenProject,
  theme,
  onToggleTheme,
}: ProjectViewProps) {
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Project | null>(null);
  const [deleting, setDeleting] = useState<Project | null>(null);
  const [editName, setEditName] = useState('');
  const [editDescription, setEditDescription] = useState('');
  const [busy, setBusy] = useState(false);
  const [localError, setLocalError] = useState('');
  const [selectedIndex, setSelectedIndex] = useState(0);
  const gridRef = useRef<HTMLElement>(null);

  useEffect(() => {
    setSelectedIndex((value) => Math.min(Math.max(value, 0), Math.max(projects.length - 1, 0)));
  }, [projects.length]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (adding || editing || deleting || projects.length === 0 || isTextEntry(event.target)) return;
      const columns = projectGridColumns(gridRef.current);
      if (event.altKey && event.key === 'Enter') {
        event.preventDefault();
        onOpenProject(projects[selectedIndex]);
        return;
      }
      switch (event.key) {
        case 'ArrowRight':
          event.preventDefault();
          setSelectedIndex((value) => Math.min(projects.length - 1, value + 1));
          break;
        case 'ArrowLeft':
          event.preventDefault();
          setSelectedIndex((value) => Math.max(0, value - 1));
          break;
        case 'ArrowDown':
          event.preventDefault();
          setSelectedIndex((value) => Math.min(projects.length - 1, value + columns));
          break;
        case 'ArrowUp':
          event.preventDefault();
          setSelectedIndex((value) => Math.max(0, value - columns));
          break;
        case 'Enter':
          event.preventDefault();
          onOpenProject(projects[selectedIndex]);
          break;
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [adding, deleting, editing, onOpenProject, projects, selectedIndex]);

  function startEdit(project: Project) {
    setEditing(project);
    setEditName(project.name);
    setEditDescription(project.description ?? '');
    setLocalError('');
  }

  async function saveEdit() {
    if (!editing || !editName.trim()) return;
    setBusy(true);
    setLocalError('');
    try {
      await api.UpdateProject(editing.id, editName, editDescription);
      setEditing(null);
      onRefresh();
    } catch (err) {
      setLocalError(String(err));
    } finally {
      setBusy(false);
    }
  }

  async function deleteProject() {
    if (!deleting) return;
    setBusy(true);
    setLocalError('');
    try {
      await api.DeleteProject(deleting.id);
      setDeleting(null);
      onRefresh();
    } catch (err) {
      setLocalError(String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="app-shell">
      <Header title="Projects" subtitle="Registered AGX workspaces" theme={theme} onToggleTheme={onToggleTheme}>
        <button className="primary-button" onClick={() => setAdding(true)}>
          Add Project
        </button>
        <IconButton label="Refresh projects" onClick={onRefresh}>
          <RefreshCw size={18} />
        </IconButton>
      </Header>
      <ErrorBar error={error || localError} />
      {projects.length === 0 ? (
        <EmptyState title="No projects" detail="Add a project to open a local git repository." />
      ) : (
        <section className="project-grid" ref={gridRef}>
          {projects.map((item, index) => (
            <article
              className={`project-card ${index === selectedIndex ? 'selected' : ''}`}
              key={item.id}
              tabIndex={0}
              style={{ animationDelay: `${Math.min(index * 35, 240)}ms` }}
              onClick={() => setSelectedIndex(index)}
              onFocus={() => setSelectedIndex(index)}
              onDoubleClick={() => onOpenProject(item)}
            >
              <Folder size={22} />
              <span className="card-title">{item.name}</span>
              <span className="path-text">{item.path}</span>
              {item.description && <span className="description-text">{item.description}</span>}
              <LanguageBars languages={item.languages ?? []} compact />
              <span className="metric-row">
                <strong>{item.taskCount}</strong> tasks
                <strong>{item.activeCount}</strong> active
                <strong>{item.waitingCount}</strong> waiting
              </span>
              <span className="project-actions" onClick={(event) => event.stopPropagation()} onDoubleClick={(event) => event.stopPropagation()}>
                <button className="text-button" onClick={() => onOpenProject(item)}>Open</button>
                <button className="text-button" onClick={() => startEdit(item)}>Edit</button>
                <button
                  className="danger-button"
                  onClick={(event) => {
                    event.stopPropagation();
                    setDeleting(item);
                  }}
                >
                  Delete
                </button>
              </span>
            </article>
          ))}
        </section>
      )}
      {adding && (
        <AddProjectModal
          candidateLimit={candidateLimit}
          onCancel={() => setAdding(false)}
          onCreated={(created) => {
            setAdding(false);
            onRefresh();
            if (openProjectAfterAdd) onOpenProject(created);
          }}
        />
      )}
      {editing && (
        <div className="modal-backdrop" onMouseDown={() => setEditing(null)}>
          <section className="project-edit-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Edit Project</h2>
            <label>
              Project name
              <input value={editName} onChange={(event) => setEditName(event.target.value)} />
            </label>
            <label>
              Description
              <textarea value={editDescription} onChange={(event) => setEditDescription(event.target.value)} />
            </label>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setEditing(null)}>Cancel</button>
              <button className="primary-button" disabled={busy || !editName.trim()} onClick={saveEdit}>Save</button>
            </div>
          </section>
        </div>
      )}
      {deleting && (
        <div className="modal-backdrop blurred" onMouseDown={() => setDeleting(null)}>
          <section className="confirm-modal" onMouseDown={(event) => event.stopPropagation()}>
            <h2>Delete Project</h2>
            <p>Delete "{deleting.name}" from AGX? This stops sessions and removes task worktrees.</p>
            <div className="wizard-actions">
              <button className="text-button" onClick={() => setDeleting(null)}>Cancel</button>
              <button className="danger-button" disabled={busy} onClick={deleteProject}>Delete</button>
            </div>
          </section>
        </div>
      )}
    </main>
  );
}

function AddProjectModal({
  candidateLimit,
  onCancel,
  onCreated,
}: {
  candidateLimit: number;
  onCancel: () => void;
  onCreated: (project: Project) => void;
}) {
  const [candidates, setCandidates] = useState<ProjectCandidate[]>([]);
  const [selected, setSelected] = useState<ProjectCandidate | null>(null);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [filter, setFilter] = useState('');
  const [manualPath, setManualPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [loadingCandidates, setLoadingCandidates] = useState(true);
  const [localError, setLocalError] = useState('');
  const nameRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let cancelled = false;
    setLoadingCandidates(true);
    api.ListProjectCandidates(candidateLimit)
      .then((items) => {
        if (!cancelled) setCandidates(items);
      })
      .catch((err) => {
        if (!cancelled) setLocalError(errorMessage(err));
      })
      .finally(() => {
        if (!cancelled) setLoadingCandidates(false);
      });
    return () => {
      cancelled = true;
    };
  }, [candidateLimit]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        onCancel();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [onCancel]);

  useEffect(() => {
    if (selected) {
      window.setTimeout(() => nameRef.current?.focus(), 0);
    }
  }, [selected]);

  function chooseCandidate(candidate: ProjectCandidate) {
    setSelected(candidate);
    setName(candidate.name);
    setDescription(candidate.description ?? '');
    setManualPath(candidate.path);
    setLocalError('');
  }

  async function openBrowser() {
    setBusy(true);
    setLocalError('');
    try {
      let start = manualPath.trim() || selected?.path || '';
      if (!start) start = await api.HomeDirectory();
      const path = await api.SelectProjectDirectory(start);
      if (path.trim()) await usePath(path);
    } catch (err) {
      setLocalError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function usePath(path: string) {
    const target = path.trim();
    if (!target) return;
    setBusy(true);
    setLocalError('');
    try {
      const candidate = await api.ValidateProjectDirectory(target);
      chooseCandidate(candidate);
    } catch (err) {
      setLocalError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  async function createProject() {
    if (!selected || !name.trim()) return;
    setBusy(true);
    setLocalError('');
    try {
      const project = await api.RegisterProject(selected.path, name, description);
      onCreated(project);
    } catch (err) {
      setLocalError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  const filteredCandidates = candidates.filter((candidate) => {
    const query = filter.trim().toLowerCase();
    if (!query) return true;
    return `${candidate.name} ${candidate.path}`.toLowerCase().includes(query);
  });

  return (
    <div
      className="modal-backdrop blurred"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onCancel();
      }}
    >
      <section className="project-add-modal" onMouseDown={(event) => event.stopPropagation()}>
        <header className="modal-header">
          <div>
            <h2>Add Git Project</h2>
            <p>Pick a discovered repository or browse to a local Git checkout.</p>
          </div>
          <IconButton label="Close" onClick={onCancel}>
            <X size={18} />
          </IconButton>
        </header>
        {localError && (
          <div className="modal-error">
            <span>{localError}</span>
          </div>
        )}
        <div className="project-add-grid">
          <section className="candidate-panel">
            <div className="candidate-toolbar">
              <label className="search-input">
                <Search size={15} />
                <input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Filter repositories" />
              </label>
              <button
                className="text-button icon-text-button"
                onMouseDown={(event) => event.stopPropagation()}
                onClick={openBrowser}
                disabled={busy}
              >
                <FolderOpen size={16} />
                Browse
              </button>
            </div>
            <div className="path-picker">
              <input
                value={manualPath}
                onChange={(event) => setManualPath(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') void usePath(manualPath);
                }}
                placeholder="Paste any Git repository path under your home directory"
              />
              <button className="text-button" disabled={busy || !manualPath.trim()} onClick={() => usePath(manualPath)}>
                Use Path
              </button>
            </div>
            <div className="candidate-list">
              {loadingCandidates ? (
                <div className="candidate-empty">Scanning your home directory for Git repositories...</div>
              ) : filteredCandidates.length === 0 ? (
                <div className="candidate-empty">No unregistered Git repositories found.</div>
              ) : (
                filteredCandidates.map((candidate) => (
                  <button
                    className={`candidate-card ${selected?.path === candidate.path ? 'selected' : ''}`}
                    key={candidate.path}
                    onClick={() => usePath(candidate.path)}
                  >
                    <span className="candidate-title">
                      <Folder size={17} />
                      {candidate.name}
                    </span>
                    <span className="candidate-path">{candidate.path}</span>
                    <LanguageBars languages={candidate.languages ?? []} compact />
                  </button>
                ))
              )}
            </div>
          </section>
          <section className="project-details-panel">
            {selected ? (
              <>
                <div className="selected-repo-card">
                  <CheckCircle2 size={18} />
                  <div>
                    <strong>{selected.name}</strong>
                    <span>{selected.path}</span>
                  </div>
                </div>
                <LanguageBars languages={selected.languages ?? []} />
                <label>
                  Project name
                  <input ref={nameRef} value={name} onChange={(event) => setName(event.target.value)} placeholder="Project name" />
                </label>
                <label>
                  Description
                  <textarea value={description} onChange={(event) => setDescription(event.target.value)} placeholder="Optional project description" />
                </label>
              </>
            ) : (
              <div className="details-empty">
                <Code2 size={28} />
                <strong>Select a repository</strong>
                <span>AGX only accepts folders that pass Git repository validation.</span>
              </div>
            )}
          </section>
        </div>
        <footer className="wizard-actions">
          <button className="text-button" onClick={onCancel}>Cancel</button>
          <button className="primary-button" onClick={createProject} disabled={busy || !selected || !name.trim()}>
            Add Project
          </button>
        </footer>
      </section>
    </div>
  );
}

function LanguageBars({ languages, compact = false }: { languages: LanguageStat[]; compact?: boolean }) {
  if (languages.length === 0) {
    return compact ? null : <div className="language-empty">No language data yet</div>;
  }
  return (
    <div className={`language-block ${compact ? 'compact' : ''}`}>
      <div className="language-stack" aria-hidden="true">
        {languages.map((language) => (
          <span
            className={`language-slice ${languageClass(language.name)}`}
            key={language.name}
            style={{ width: `${Math.max(language.percentage, 3)}%` }}
          />
        ))}
      </div>
      <div className="language-list">
        {languages.map((language) => (
          <span key={language.name}>
            <i className={`language-dot ${languageClass(language.name)}`} />
            {language.name} {language.percentage}%
          </span>
        ))}
      </div>
    </div>
  );
}

function languageClass(name: string): string {
  switch (name) {
    case 'C++':
      return 'lang-cpp';
    case 'C#':
      return 'lang-csharp';
    default:
      return `lang-${name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`;
  }
}
