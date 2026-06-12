import type React from 'react';

export function appendPromptPaths(current: string, paths: string[]): string {
  const text = paths.filter(Boolean).join(' ');
  if (!text) return current;
  return `${current}${current ? ' ' : ''}${text}`;
}

export function addUniquePaths(current: string[], paths: string[]): string[] {
  return Array.from(new Set([...current, ...paths.filter(Boolean)]));
}

export function setDragPaths(event: React.DragEvent, paths: string[]) {
  const unique = Array.from(new Set(paths.filter(Boolean)));
  event.dataTransfer.effectAllowed = 'copy';
  event.dataTransfer.setData('application/x-agx-paths', JSON.stringify(unique));
  event.dataTransfer.setData('text/plain', unique.join(' '));
}

export function pathsFromDrop(event: React.DragEvent): string[] {
  const encoded = event.dataTransfer.getData('application/x-agx-paths');
  if (encoded) {
    try {
      const parsed = JSON.parse(encoded);
      if (Array.isArray(parsed)) return parsed.filter((item): item is string => typeof item === 'string' && item !== '');
    } catch {
      return [];
    }
  }
  const text = event.dataTransfer.getData('text/plain').trim();
  return text ? [text] : [];
}
