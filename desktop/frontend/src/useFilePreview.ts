import { useCallback, useEffect, useRef, useState } from 'react';

import { api } from './api';

const defaultAutoRefreshInterval = 2000;

type PreviewLoadMode = 'initial' | 'manual' | 'auto';

export function useFilePreview(taskId: string, active: boolean, autoRefreshInterval = defaultAutoRefreshInterval) {
  const [path, setPath] = useState('');
  const [content, setContent] = useState('');
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState('');
  const pathRef = useRef('');
  const contentRef = useRef('');
  const taskIdRef = useRef(taskId);
  const requestSequenceRef = useRef(0);
  const mountedRef = useRef(true);
  const inFlightRef = useRef<{ path: string; promise: Promise<void> } | null>(null);

  const loadPath = useCallback(async (nextPath: string, mode: PreviewLoadMode) => {
    if (!nextPath) return;

    const existing = inFlightRef.current;
    if (existing?.path === nextPath) {
      if (mode === 'manual') setRefreshing(true);
      await existing.promise;
      if (mode === 'manual' && mountedRef.current) setRefreshing(false);
      return;
    }

    const requestTaskId = taskIdRef.current;
    const requestSequence = ++requestSequenceRef.current;
    if (mode === 'initial') setLoading(true);
    if (mode === 'manual') setRefreshing(true);

    const promise = (async () => {
      try {
        const nextContent = await api.ReadTaskFile(requestTaskId, nextPath);
        if (
          !mountedRef.current
          || requestSequence !== requestSequenceRef.current
          || taskIdRef.current !== requestTaskId
          || pathRef.current !== nextPath
        ) return;

        if (nextContent !== contentRef.current) {
          contentRef.current = nextContent;
          setContent(nextContent);
        }
        setError('');
      } catch (err) {
        if (
          mountedRef.current
          && requestSequence === requestSequenceRef.current
          && taskIdRef.current === requestTaskId
          && pathRef.current === nextPath
        ) {
          setError(err instanceof Error ? err.message : String(err));
        }
      } finally {
        if (mountedRef.current && requestSequence === requestSequenceRef.current) {
          if (mode === 'initial') setLoading(false);
          if (mode === 'manual') setRefreshing(false);
        }
      }
    })();

    inFlightRef.current = { path: nextPath, promise };
    await promise;
    if (inFlightRef.current?.promise === promise) inFlightRef.current = null;
  }, []);

  const openFile = useCallback((nextPath: string) => {
    requestSequenceRef.current += 1;
    pathRef.current = nextPath;
    contentRef.current = '';
    setPath(nextPath);
    setContent('');
    setError('');
    void loadPath(nextPath, 'initial');
  }, [loadPath]);

  const refreshFile = useCallback(() => loadPath(pathRef.current, 'manual'), [loadPath]);

  const closeFile = useCallback(() => {
    requestSequenceRef.current += 1;
    pathRef.current = '';
    contentRef.current = '';
    setPath('');
    setContent('');
    setLoading(false);
    setRefreshing(false);
    setError('');
  }, []);

  useEffect(() => {
    taskIdRef.current = taskId;
    closeFile();
  }, [closeFile, taskId]);

  useEffect(() => {
    if (!active || !path) return;

    const refreshIfVisible = () => {
      if (!document.hidden) void loadPath(path, 'auto');
    };
    const onVisibilityChange = () => {
      if (!document.hidden) refreshIfVisible();
    };

    refreshIfVisible();
    const timer = window.setInterval(refreshIfVisible, autoRefreshInterval);
    document.addEventListener('visibilitychange', onVisibilityChange);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [active, autoRefreshInterval, loadPath, path]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      requestSequenceRef.current += 1;
    };
  }, []);

  return {
    path,
    content,
    loading,
    refreshing,
    error,
    openFile,
    refreshFile,
    closeFile,
  };
}
