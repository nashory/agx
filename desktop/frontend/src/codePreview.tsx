import { useEffect, useMemo, useState } from 'react';
import DOMPurify from 'dompurify';
import hljs from 'highlight.js/lib/core';
import bash from 'highlight.js/lib/languages/bash';
import c from 'highlight.js/lib/languages/c';
import cpp from 'highlight.js/lib/languages/cpp';
import css from 'highlight.js/lib/languages/css';
import dockerfile from 'highlight.js/lib/languages/dockerfile';
import go from 'highlight.js/lib/languages/go';
import ini from 'highlight.js/lib/languages/ini';
import java from 'highlight.js/lib/languages/java';
import javascript from 'highlight.js/lib/languages/javascript';
import json from 'highlight.js/lib/languages/json';
import makefile from 'highlight.js/lib/languages/makefile';
import markdown from 'highlight.js/lib/languages/markdown';
import python from 'highlight.js/lib/languages/python';
import ruby from 'highlight.js/lib/languages/ruby';
import rust from 'highlight.js/lib/languages/rust';
import sql from 'highlight.js/lib/languages/sql';
import typescript from 'highlight.js/lib/languages/typescript';
import xml from 'highlight.js/lib/languages/xml';
import yaml from 'highlight.js/lib/languages/yaml';

hljs.registerLanguage('bash', bash);
hljs.registerLanguage('c', c);
hljs.registerLanguage('cpp', cpp);
hljs.registerLanguage('css', css);
hljs.registerLanguage('dockerfile', dockerfile);
hljs.registerLanguage('go', go);
hljs.registerLanguage('ini', ini);
hljs.registerLanguage('java', java);
hljs.registerLanguage('javascript', javascript);
hljs.registerLanguage('json', json);
hljs.registerLanguage('makefile', makefile);
hljs.registerLanguage('markdown', markdown);
hljs.registerLanguage('python', python);
hljs.registerLanguage('ruby', ruby);
hljs.registerLanguage('rust', rust);
hljs.registerLanguage('sql', sql);
hljs.registerLanguage('typescript', typescript);
hljs.registerLanguage('xml', xml);
hljs.registerLanguage('yaml', yaml);

const maxHighlightChars = 200 * 1024;

const languageByExtension: Record<string, string> = {
  c: 'c',
  cc: 'cpp',
  cpp: 'cpp',
  css: 'css',
  go: 'go',
  h: 'c',
  hpp: 'cpp',
  html: 'xml',
  java: 'java',
  js: 'javascript',
  json: 'json',
  jsx: 'javascript',
  lock: 'json',
  md: 'markdown',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  sh: 'bash',
  sql: 'sql',
  toml: 'ini',
  ts: 'typescript',
  tsx: 'typescript',
  yaml: 'yaml',
  yml: 'yaml',
};

function languageForPath(path: string): string | undefined {
  const filename = path.split('/').pop()?.toLowerCase() ?? '';
  if (filename === 'dockerfile') return 'dockerfile';
  if (filename === 'makefile') return 'makefile';
  const ext = filename.includes('.') ? filename.split('.').pop() : '';
  return ext ? languageByExtension[ext] : undefined;
}

export function isMarkdownPreviewPath(path: string): boolean {
  const filename = path.split('/').pop()?.toLowerCase() ?? '';
  return filename.endsWith('.md') || filename.endsWith('.markdown');
}

function escapeHTML(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function inlineMarkdown(value: string): string {
  let html = escapeHTML(value);
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');
  html = html.replace(/\[([^\]]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>');
  return html;
}

function splitMarkdownTableRow(line: string): string[] {
  const trimmed = line.trim().replace(/^\|/, '').replace(/\|$/, '');
  const cells: string[] = [];
  let current = '';
  let escaped = false;

  for (const char of trimmed) {
    if (escaped) {
      current += char;
      escaped = false;
      continue;
    }
    if (char === '\\') {
      escaped = true;
      continue;
    }
    if (char === '|') {
      cells.push(current.trim());
      current = '';
      continue;
    }
    current += char;
  }
  cells.push(current.trim());
  return cells;
}

function isMarkdownTableSeparator(line: string): boolean {
  const cells = splitMarkdownTableRow(line);
  return cells.length > 1 && cells.every((cell) => /^:?-{3,}:?$/.test(cell.replace(/\s+/g, '')));
}

function isMarkdownTableStart(lines: string[], index: number): boolean {
  const header = lines[index]?.trim() ?? '';
  const separator = lines[index + 1]?.trim() ?? '';
  return header.includes('|') && isMarkdownTableSeparator(separator);
}

function tableAlignClass(separator: string): string {
  const compact = separator.replace(/\s+/g, '');
  if (compact.startsWith(':') && compact.endsWith(':')) return ' class="align-center"';
  if (compact.endsWith(':')) return ' class="align-right"';
  if (compact.startsWith(':')) return ' class="align-left"';
  return '';
}

function renderMarkdownTable(lines: string[], start: number): { html: string; nextIndex: number } {
  const headers = splitMarkdownTableRow(lines[start]);
  const separators = splitMarkdownTableRow(lines[start + 1]);
  const columnCount = Math.max(headers.length, separators.length);
  const aligns = Array.from({ length: columnCount }, (_, index) => tableAlignClass(separators[index] ?? '---'));
  const bodyRows: string[][] = [];
  let nextIndex = start + 2;

  while (nextIndex < lines.length) {
    const line = lines[nextIndex];
    const trimmed = line.trim();
    if (!trimmed || !trimmed.includes('|') || trimmed.startsWith('```')) break;
    bodyRows.push(splitMarkdownTableRow(line));
    nextIndex += 1;
  }

  const normalizedCell = (cells: string[], index: number) => inlineMarkdown(cells[index] ?? '');
  const headerCells = Array.from(
    { length: columnCount },
    (_, index) => `<th${aligns[index]}>${normalizedCell(headers, index)}</th>`,
  ).join('');
  const body = bodyRows
    .map((row) => `<tr>${Array.from({ length: columnCount }, (_, index) => `<td${aligns[index]}>${normalizedCell(row, index)}</td>`).join('')}</tr>`)
    .join('');
  return {
    html: `<table><thead><tr>${headerCells}</tr></thead>${body ? `<tbody>${body}</tbody>` : ''}</table>`,
    nextIndex,
  };
}

function cleanHighlightHTML(html: string): string {
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ['span'],
    ALLOWED_ATTR: ['class'],
  });
}

function highlightedFence(lang: string, code: string): string {
  const language = lang.trim().toLowerCase();
  if (code.length > maxHighlightChars) {
    return escapeHTML(code);
  }
  try {
    if (language && hljs.getLanguage(language)) {
      return cleanHighlightHTML(hljs.highlight(code, { language }).value);
    }
    return cleanHighlightHTML(hljs.highlightAuto(code).value);
  } catch {
    return escapeHTML(code);
  }
}

export function renderMarkdown(content: string, options: { preserveLineBreaks?: boolean } = {}): string {
  const lines = content.replace(/\r\n/g, '\n').split('\n');
  const out: string[] = [];
  let inList = false;
  let inCode = false;
  let codeLang = '';
  let codeLines: string[] = [];
  let paragraph: string[] = [];

  const flushParagraph = () => {
    if (paragraph.length === 0) return;
    const body = options.preserveLineBreaks
      ? paragraph.map((line) => inlineMarkdown(line)).join('<br>')
      : inlineMarkdown(paragraph.join(' '));
    out.push(`<p>${body}</p>`);
    paragraph = [];
  };
  const closeList = () => {
    if (!inList) return;
    out.push('</ul>');
    inList = false;
  };

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trim();
    if (trimmed.startsWith('```')) {
      flushParagraph();
      closeList();
      if (inCode) {
        out.push(highlightedFence(codeLang, codeLines.join('\n')));
        out.push('</code></pre>');
        inCode = false;
        codeLang = '';
        codeLines = [];
      } else {
        codeLang = trimmed.slice(3).trim();
        out.push(`<pre><code${codeLang ? ` class="language-${escapeHTML(codeLang)}"` : ''}>`);
        inCode = true;
        codeLines = [];
      }
      continue;
    }
    if (inCode) {
      codeLines.push(line);
      continue;
    }
    if (trimmed === '') {
      flushParagraph();
      closeList();
      continue;
    }
    if (isMarkdownTableStart(lines, index)) {
      flushParagraph();
      closeList();
      const table = renderMarkdownTable(lines, index);
      out.push(table.html);
      index = table.nextIndex - 1;
      continue;
    }
    const heading = /^(#{1,6})\s+(.+)$/.exec(trimmed);
    if (heading) {
      flushParagraph();
      closeList();
      const level = heading[1].length;
      out.push(`<h${level}>${inlineMarkdown(heading[2])}</h${level}>`);
      continue;
    }
    const bullet = /^[-*]\s+(.+)$/.exec(trimmed);
    if (bullet) {
      flushParagraph();
      if (!inList) {
        out.push('<ul>');
        inList = true;
      }
      out.push(`<li>${inlineMarkdown(bullet[1])}</li>`);
      continue;
    }
    closeList();
    paragraph.push(trimmed);
  }
  flushParagraph();
  closeList();
  if (inCode) {
    out.push(highlightedFence(codeLang, codeLines.join('\n')));
    out.push('</code></pre>');
  }
  return DOMPurify.sanitize(out.join('\n'), {
    ALLOWED_TAGS: ['a', 'br', 'code', 'em', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'li', 'p', 'pre', 'span', 'strong', 'table', 'tbody', 'td', 'th', 'thead', 'tr', 'ul'],
    ALLOWED_ATTR: ['class', 'href', 'rel', 'target'],
  });
}

function highlightedCode(path: string, content: string): { html: string; language: string } {
  if (content.length > maxHighlightChars) {
    return { html: escapeHTML(content), language: 'text (highlight skipped)' };
  }
  const language = languageForPath(path);
  try {
    if (language && hljs.getLanguage(language)) {
      return { html: cleanHighlightHTML(hljs.highlight(content, { language }).value), language };
    }
    const result = hljs.highlightAuto(content);
    return { html: cleanHighlightHTML(result.value), language: result.language ?? 'text' };
  } catch {
    return { html: escapeHTML(content), language: 'text' };
  }
}

function lineContextReference(path: string, start: number, end: number): string {
  const first = Math.min(start, end);
  const last = Math.max(start, end);
  return first === last ? `${path}:L${first}` : `${path}:L${first}-L${last}`;
}

export function CodePreview({ path, content, onAddContext, renderMarkdown: renderMarkdownView = false }: { path: string; content: string; onAddContext?: (reference: string) => void; renderMarkdown?: boolean }) {
  const highlighted = useMemo(() => highlightedCode(path, content), [path, content]);
  const markdown = isMarkdownPreviewPath(path);
  const renderedMarkdown = useMemo(() => (markdown ? renderMarkdown(content) : ''), [content, markdown]);
  const rendered = markdown && renderMarkdownView;
  const lineCount = Math.max(1, content.split('\n').length);
  const numbers = Array.from({ length: lineCount }, (_, index) => index + 1);
  const [dragStart, setDragStart] = useState<number | null>(null);
  const [dragEnd, setDragEnd] = useState<number | null>(null);
  const selectedStart = dragStart === null || dragEnd === null ? 0 : Math.min(dragStart, dragEnd);
  const selectedEnd = dragStart === null || dragEnd === null ? 0 : Math.max(dragStart, dragEnd);

  useEffect(() => {
    if (dragStart === null || dragEnd === null) return;
    const onMouseUp = () => {
      onAddContext?.(lineContextReference(path, dragStart, dragEnd));
      setDragStart(null);
      setDragEnd(null);
    };
    window.addEventListener('mouseup', onMouseUp);
    return () => window.removeEventListener('mouseup', onMouseUp);
  }, [dragStart, dragEnd, onAddContext, path]);

  return (
    <div className="code-preview">
      <div className="code-preview-meta">{rendered ? 'rendered markdown' : highlighted.language}</div>
      {rendered && markdown ? (
        <article className="markdown-preview" dangerouslySetInnerHTML={{ __html: renderedMarkdown || ' ' }} />
      ) : (
        <div className="code-preview-scroll">
        <div className="line-numbers" aria-label="Line numbers">
          {numbers.map((line) => (
            <button
              type="button"
              key={line}
              className={line >= selectedStart && line <= selectedEnd ? 'selected' : ''}
              onMouseDown={(event) => {
                event.preventDefault();
                setDragStart(line);
                setDragEnd(line);
              }}
              onMouseEnter={() => {
                if (dragStart !== null) setDragEnd(line);
              }}
            >
              {line}
            </button>
          ))}
        </div>
        <pre className="code-content">
          <code dangerouslySetInnerHTML={{ __html: highlighted.html || ' ' }} />
        </pre>
      </div>
      )}
    </div>
  );
}
