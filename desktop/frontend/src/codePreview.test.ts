import { describe, expect, it } from 'vitest';
import { renderMarkdown } from './codePreview';

describe('renderMarkdown', () => {
  it('renders GitHub-style markdown tables', () => {
    const html = renderMarkdown([
      '| Name | Status |',
      '| --- | ---: |',
      '| Discord sync | **OK** |',
      '| Cleanup | `done` |',
    ].join('\n'));

    expect(html).toContain('<table>');
    expect(html).toContain('<thead>');
    expect(html).toContain('<th>Name</th>');
    expect(html).toContain('<th class="align-right">Status</th>');
    expect(html).toContain('<td>Discord sync</td>');
    expect(html).toContain('<td class="align-right"><strong>OK</strong></td>');
    expect(html).toContain('<td class="align-right"><code>done</code></td>');
  });

  it('keeps tables separate from surrounding paragraphs', () => {
    const html = renderMarkdown([
      'Before',
      '',
      '| Left | Center |',
      '| :--- | :---: |',
      '| one | two |',
      '',
      'After',
    ].join('\n'), { preserveLineBreaks: true });

    expect(html).toContain('<p>Before</p>');
    expect(html).toContain('<table>');
    expect(html).toContain('<th class="align-left">Left</th>');
    expect(html).toContain('<th class="align-center">Center</th>');
    expect(html).toContain('<p>After</p>');
  });
});
