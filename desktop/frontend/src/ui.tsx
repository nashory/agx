import React, { useCallback, useEffect, useState } from 'react';
import {
  Grid2X2,
  List,
  Maximize2,
  Minimize2,
  Moon,
  Sun,
} from 'lucide-react';
import type { ViewMode } from './types';

export type ThemeMode = 'dark' | 'light';

export function Header({ title, subtitle, detail, theme, onToggleTheme, children }: { title: string; subtitle: string; detail?: string; theme: ThemeMode; onToggleTheme: () => void; children?: React.ReactNode }) {
  const [isFullscreen, setIsFullscreen] = useState(false);

  useEffect(() => {
    let alive = true;
    const check = async () => {
      try {
        const fullscreen = await window.runtime?.WindowIsFullscreen?.();
        if (alive && typeof fullscreen === 'boolean') setIsFullscreen(fullscreen);
      } catch {
        // Wails fullscreen state is best-effort on startup.
      }
    };
    void check();
    return () => {
      alive = false;
    };
  }, []);

  const toggleFullscreen = useCallback(() => {
    if (isFullscreen) {
      window.runtime?.WindowUnfullscreen?.();
      setIsFullscreen(false);
      return;
    }
    window.runtime?.WindowFullscreen?.();
    setIsFullscreen(true);
  }, [isFullscreen]);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === 'F11' || ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'f')) {
        event.preventDefault();
        toggleFullscreen();
      }
    }
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [toggleFullscreen]);

  return (
    <header className="topbar">
      <div>
        <h1>{title}</h1>
        <p>{subtitle}</p>
        {detail && <p className="topbar-detail">{detail}</p>}
      </div>
      <div className="topbar-actions">
        <IconButton label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'} onClick={onToggleTheme}>
          {theme === 'dark' ? <Sun size={18} /> : <Moon size={18} />}
        </IconButton>
        <IconButton label={isFullscreen ? 'Exit fullscreen' : 'Enter fullscreen'} onClick={toggleFullscreen}>
          {isFullscreen ? <Minimize2 size={18} /> : <Maximize2 size={18} />}
        </IconButton>
        {children}
      </div>
    </header>
  );
}

export function Segmented({ value, onChange }: { value: ViewMode; onChange: (mode: ViewMode) => void }) {
  return (
    <div className="segmented">
      <IconButton label="Grid view" active={value === 'grid'} onClick={() => onChange('grid')}>
        <Grid2X2 size={17} />
      </IconButton>
      <IconButton label="List view" active={value === 'list'} onClick={() => onChange('list')}>
        <List size={17} />
      </IconButton>
    </div>
  );
}

export function IconButton({ label, active, disabled, onClick, children }: { label: string; active?: boolean; disabled?: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button className={`icon-button ${active ? 'active' : ''}`} title={label} aria-label={label} disabled={disabled} onClick={onClick}>
      {children}
    </button>
  );
}

export function ErrorBar({ error }: { error: string }) {
  return error ? <div className="error-bar">{error}</div> : null;
}

export function EmptyState({ title, detail }: { title: string; detail: string }) {
  return (
    <section className="empty-state">
      <h2>{title}</h2>
      <p>{detail}</p>
    </section>
  );
}
