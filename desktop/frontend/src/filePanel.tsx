import React, { useCallback, useEffect, useRef, useState } from 'react';
import { ChevronDown, ChevronRight, File as FileIcon, Folder, FolderOpen } from 'lucide-react';
import { api } from './api';
import { setDragPaths } from './pathDrag';
import type { FileEntry, Project } from './types';

type FilePanelProps = {
  project: Project;
  taskId?: string;
  rootPath?: string;
  onInsertPaths: (paths: string[]) => void;
  onContextPaths: (paths: string[]) => void;
  onPreview: (path: string) => void;
};

export function FilePanel({ project, taskId, rootPath, onInsertPaths, onContextPaths, onPreview }: FilePanelProps) {
  const [entriesByDir, setEntriesByDir] = useState<Record<string, FileEntry[]>>({});
  const [expanded, setExpanded] = useState<Record<string, boolean>>({ '.': true });
  const [search, setSearch] = useState('');
  const [matches, setMatches] = useState<string[]>([]);
  const [selectedPaths, setSelectedPaths] = useState<string[]>([]);
  const [menu, setMenu] = useState<{ x: number; y: number; entry: FileEntry; paths: string[] } | null>(null);
  const [error, setError] = useState('');
  const searchRef = useRef<HTMLInputElement>(null);
  const searchRequest = useRef(0);
  const rootKey = `${taskId ?? project.id}:${rootPath ?? project.path}`;

  const loadDirectory = useCallback(async (dir: string) => {
    try {
      const entries = taskId
        ? await api.ListTaskDirectory(taskId, dir, false)
        : await api.ListDirectory(project.id, dir, false);
      setError('');
      setEntriesByDir((value) => ({ ...value, [dir]: entries }));
    } catch (err) {
      setError(String(err));
      if (dir === '.') {
        setEntriesByDir((value) => ({ ...value, [dir]: [] }));
      }
    }
  }, [project.id, taskId]);

  useEffect(() => {
    searchRequest.current += 1;
    setEntriesByDir({});
    setExpanded({ '.': true });
    setMatches([]);
    setSelectedPaths([]);
    setSearch('');
    setError('');
  }, [rootKey]);

  useEffect(() => {
    void loadDirectory('.');
  }, [loadDirectory]);

  useEffect(() => {
    const refreshExpandedDirectories = () => {
      if (document.hidden) return;
      const dirs = Object.entries(expanded)
        .filter(([, isExpanded]) => isExpanded)
        .map(([dir]) => dir);
      for (const dir of dirs) {
        void loadDirectory(dir).catch(() => undefined);
      }
    };
    const timer = window.setInterval(refreshExpandedDirectories, 2500);
    window.addEventListener('focus', refreshExpandedDirectories);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', refreshExpandedDirectories);
    };
  }, [expanded, loadDirectory]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'p') {
        event.preventDefault();
        searchRef.current?.focus();
        searchRef.current?.select();
      }
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, []);

  useEffect(() => {
    if (!search.trim()) {
      setMatches([]);
      return;
    }
    const requestID = ++searchRequest.current;
    const query = search.trim();
    const timer = window.setTimeout(() => {
      const searchFiles = taskId
        ? api.SearchTaskFiles(taskId, query, 20)
        : api.SearchFiles(project.id, query, 20);
      void searchFiles.then((results) => {
        if (requestID === searchRequest.current) {
          setError('');
          setMatches(results);
        }
      }).catch((err) => {
        if (requestID === searchRequest.current) {
          setError(String(err));
          setMatches([]);
        }
      });
    }, 120);
    return () => window.clearTimeout(timer);
  }, [project.id, search, taskId]);

  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    window.addEventListener('click', close);
    window.addEventListener('keydown', close);
    return () => {
      window.removeEventListener('click', close);
      window.removeEventListener('keydown', close);
    };
  }, [menu]);

  function toggleDirectory(dir: string) {
    const next = !expanded[dir];
    setExpanded((value) => ({ ...value, [dir]: next }));
    if (next && !entriesByDir[dir]) {
      void loadDirectory(dir);
    }
  }

  function selectedPathsForEntry(entry: FileEntry): string[] {
    return selectedPaths.includes(entry.path) ? selectedPaths : [entry.path];
  }

  function absolutePath(relativePath: string): string {
    const root = (rootPath ?? project.path).replace(/[\\/]+$/, '');
    if (relativePath === '.' || relativePath === '') return root;
    return `${root}/${relativePath}`;
  }

  async function copyPaths(paths: string[], mode: 'absolute' | 'relative') {
    const text = paths.map((path) => (mode === 'absolute' ? absolutePath(path) : path)).join('\n');
    await navigator.clipboard.writeText(text);
    setMenu(null);
  }

  function selectEntry(entry: FileEntry, event: React.MouseEvent) {
    if (event.metaKey || event.ctrlKey) {
      setSelectedPaths((value) => (
        value.includes(entry.path)
          ? value.filter((path) => path !== entry.path)
          : [...value, entry.path]
      ));
      return;
    }
    if (entry.isDir) {
      if (event.detail > 1) return;
      setSelectedPaths([entry.path]);
      toggleDirectory(entry.path);
      return;
    }
    setSelectedPaths([entry.path]);
    onPreview(entry.path);
  }

  function renderEntry(entry: FileEntry, depth: number): React.ReactNode {
    const isExpanded = Boolean(expanded[entry.path]);
    const isSelected = selectedPaths.includes(entry.path);
    return (
      <div className="file-entry" key={entry.path}>
        <button
          className={`explorer-row ${entry.isDir ? 'directory' : 'file'} ${isSelected ? 'selected' : ''}`}
          style={{ paddingLeft: 8 + depth * 14 }}
          draggable
          onDragStart={(event) => setDragPaths(event, selectedPathsForEntry(entry))}
          onDoubleClick={() => (!entry.isDir ? onInsertPaths(selectedPathsForEntry(entry)) : undefined)}
          onClick={(event) => selectEntry(entry, event)}
          onContextMenu={(event) => {
            event.preventDefault();
            const paths = selectedPathsForEntry(entry);
            setSelectedPaths(paths);
            setMenu({ x: event.clientX, y: event.clientY, entry, paths });
          }}
        >
          <span className="explorer-chevron">{entry.isDir ? (isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />) : null}</span>
          <span className="explorer-icon">{entry.isDir ? (isExpanded ? <FolderOpen size={15} /> : <Folder size={15} />) : <FileIcon size={14} />}</span>
          <span className="explorer-name">{entry.name}</span>
        </button>
        {entry.isDir && isExpanded && entriesByDir[entry.path]?.map((child) => renderEntry(child, depth + 1))}
      </div>
    );
  }

  const rootEntries = matches.length > 0
    ? matches.map((match) => ({ name: match, path: match, isDir: false }))
    : entriesByDir['.'] ?? [];

  return (
    <aside className="file-panel">
      <input ref={searchRef} value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Search files" />
      {error && <div className="file-panel-error">{error}</div>}
      <div className="file-list">
        {rootEntries.map((entry) => renderEntry(entry, 0))}
      </div>
      {menu && (
        <div className="file-menu" style={{ left: menu.x, top: menu.y }} onClick={(event) => event.stopPropagation()}>
          <button onClick={() => { onContextPaths(menu.paths); setMenu(null); }}>Add to context</button>
          <button onClick={() => void copyPaths(menu.paths, 'absolute')}>Copy path</button>
          <button onClick={() => void copyPaths(menu.paths, 'relative')}>Copy relative path</button>
          {!menu.entry.isDir && <button onClick={() => { onPreview(menu.entry.path); setMenu(null); }}>Preview</button>}
        </div>
      )}
    </aside>
  );
}
