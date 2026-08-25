import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { api } from './api';
import { useFilePreview } from './useFilePreview';

vi.mock('./api', () => ({
  api: {
    ReadTaskFile: vi.fn(),
  },
}));

async function flushPreviewLoad() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe('useFilePreview', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  it('refreshes the preview when the file changes', async () => {
    vi.mocked(api.ReadTaskFile)
      .mockResolvedValueOnce('first version')
      .mockResolvedValueOnce('second version');
    const { result } = renderHook(() => useFilePreview('task-1', true, 1000));

    act(() => result.current.openFile('notes.md'));
    await flushPreviewLoad();
    expect(result.current.content).toBe('first version');

    await act(async () => vi.advanceTimersByTimeAsync(1000));

    expect(result.current.content).toBe('second version');
    expect(api.ReadTaskFile).toHaveBeenCalledTimes(2);
  });

  it('keeps stable state when refreshed content is unchanged', async () => {
    vi.mocked(api.ReadTaskFile).mockResolvedValue('same version');
    const { result } = renderHook(() => useFilePreview('task-1', true, 1000));

    act(() => result.current.openFile('notes.md'));
    await flushPreviewLoad();
    const stableResult = result.current;

    await act(async () => vi.advanceTimersByTimeAsync(1000));

    expect(api.ReadTaskFile).toHaveBeenCalledTimes(2);
    expect(result.current).toBe(stableResult);
  });

  it('pauses automatic refresh while the document is hidden', async () => {
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
    vi.mocked(api.ReadTaskFile).mockResolvedValue('visible content');
    const { result } = renderHook(() => useFilePreview('task-1', true, 1000));

    act(() => result.current.openFile('notes.md'));
    await flushPreviewLoad();
    await act(async () => vi.advanceTimersByTimeAsync(3000));

    expect(api.ReadTaskFile).toHaveBeenCalledTimes(1);
    hidden.mockRestore();
  });

  it('retains the last content when an automatic refresh fails', async () => {
    vi.mocked(api.ReadTaskFile)
      .mockResolvedValueOnce('saved content')
      .mockRejectedValueOnce(new Error('file temporarily unavailable'));
    const { result } = renderHook(() => useFilePreview('task-1', true, 1000));

    act(() => result.current.openFile('notes.md'));
    await flushPreviewLoad();
    await act(async () => vi.advanceTimersByTimeAsync(1000));

    expect(result.current.content).toBe('saved content');
    expect(result.current.error).toBe('file temporarily unavailable');
  });

  it('supports an immediate manual refresh', async () => {
    vi.mocked(api.ReadTaskFile)
      .mockResolvedValueOnce('first version')
      .mockResolvedValueOnce('manual version');
    const { result } = renderHook(() => useFilePreview('task-1', false, 1000));

    act(() => result.current.openFile('notes.md'));
    await flushPreviewLoad();
    await act(async () => result.current.refreshFile());

    expect(result.current.content).toBe('manual version');
    expect(result.current.refreshing).toBe(false);
  });
});
