import { test } from 'node:test';
import assert from 'node:assert/strict';

import { htmlToMarkdown, tidyMarkdown } from '../src/markdown.js';

const BASE = 'https://example.com/posts/article';

test('converts headings, lists, emphasis and code to markdown', () => {
  const html = `
    <h1>Title</h1>
    <h2>Section</h2>
    <p>Some <strong>bold</strong> and <em>italic</em> text.</p>
    <ul><li>one</li><li>two</li></ul>
    <pre><code>def f():
    return 1</code></pre>
  `;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: true });

  assert.ok(markdown.startsWith('# Title'));
  assert.match(markdown, /## Section/);
  assert.match(markdown, /\*\*bold\*\*/);
  assert.match(markdown, /\*italic\*/);
  assert.match(markdown, /- one/);
  assert.match(markdown, /- two/);
  assert.match(markdown, /```/);
  assert.match(markdown, /    return 1/);
});

test('resolves relative links and images to absolute urls', () => {
  const html = `
    <p>See the <a href="/docs/intro">guide</a> and
       <a href="details.html">details</a>.</p>
    <img src="images/diagram.png" alt="Diagram">
  `;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: true });

  assert.match(markdown, /\[guide\]\(https:\/\/example\.com\/docs\/intro\)/);
  assert.match(markdown, /\[details\]\(https:\/\/example\.com\/posts\/details\.html\)/);
  assert.match(markdown, /!\[Diagram\]\(https:\/\/example\.com\/posts\/images\/diagram\.png\)/);
});

test('drops unsafe links, trackers and data-uri images', () => {
  const html = `
    <p><a href="javascript:alert(1)">bad</a></p>
    <img src="https://analytics.example/pixel.gif" width="1" height="1" alt="pixel">
    <img src="data:image/png;base64,AAAA" alt="spacer">
    <img src="https://cdn.example.com/photo.jpg" alt="photo">
  `;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: true });

  assert.ok(!markdown.includes('javascript:'));
  assert.ok(!markdown.includes('analytics.example'));
  assert.ok(!markdown.includes('data:image'));
  assert.match(markdown, /!\[photo\]\(https:\/\/cdn\.example\.com\/photo\.jpg\)/);
});

test('with_links=false flattens links to plain text', () => {
  const html = `<p>Read <a href="https://example.com/x">the guide</a> now.</p>`;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: false, withImages: true });

  assert.equal(markdown, 'Read the guide now.');
});

test('with_images=false removes images entirely', () => {
  const html = `<p>Text</p><img src="https://cdn.example.com/a.png" alt="a">`;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: false });

  assert.equal(markdown, 'Text');
});

test('converts tables to gfm pipe tables', () => {
  const html = `
    <table>
      <tr><th>Engine</th><th>Index</th></tr>
      <tr><td>Qdrant</td><td>HNSW</td></tr>
    </table>
  `;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: true });

  assert.match(markdown, /Engine/);
  assert.match(markdown, /Qdrant/);
  assert.match(markdown, /HNSW/);
  assert.match(markdown, /\|/);
});

test('tidyMarkdown normalizes broken links and collapses blank lines', () => {
  const input = '[ some\ntext ]( https://example.com/a )\n\n\n\nnext';
  const output = tidyMarkdown(input);

  assert.equal(output, '[some text](https://example.com/a)\n\nnext');
});

test('tidyMarkdown trims trailing whitespace and reverts benign escapes', () => {
  const output = tidyMarkdown('line one   \nline \\_two\\_ \\[brackets\\]\n');

  assert.equal(output, 'line one\nline _two_ [brackets]');
});

test('flattens links with oversized urls to their label text', () => {
  const longHref = `https://example.com/search?token=${'x'.repeat(200)}`;
  const html = `<p>See <a href="${longHref}">the guide</a> and
    <a href="https://example.com/x">details</a>.</p>`;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: true });

  assert.ok(!markdown.includes('token='));
  assert.match(markdown, /See the guide and/);
  assert.match(markdown, /\[details\]\(https:\/\/example\.com\/x\)/);
});

test('regroups fragmented figure paragraphs into a compact list', () => {
  const html = `
    <h2>Section</h2>
    <p>Intro paragraph with enough length to stay a normal paragraph block.</p>
    <p>figure title</p>
    <p>label one</p>
    <p>value with spaces</p>
    <p>· · ·</p>
    <p>Closing paragraph with enough length to stay a normal paragraph block.</p>
  `;
  const markdown = htmlToMarkdown(html, { baseUrl: BASE, withLinks: true, withImages: true });

  assert.match(markdown, /## Section/);
  assert.match(markdown, /- figure title\n- label one\n- value with spaces/);
  assert.ok(!markdown.includes('·'));
  assert.match(markdown, /^Intro paragraph/m);
  assert.match(markdown, /^Closing paragraph/m);
});

test('keeps short standalone paragraphs and soft-wrapped blocks intact', () => {
  const output = tidyMarkdown('one short line\n\nanother short line\n\nlong paragraph that stays a paragraph because it exceeds the fragment budget');

  assert.equal(
    output,
    'one short line\n\nanother short line\n\nlong paragraph that stays a paragraph because it exceeds the fragment budget',
  );
});

test('short prose paragraphs ending with sentence punctuation stay paragraphs', () => {
  const output = tidyMarkdown('第一句话说完了。\n\n第二句话也说完了。\n\n第三句话同样说完了。\n\n后续的长段落内容，远远超过碎片段落的长度预算，因此保持原样输出。');

  assert.ok(!output.includes('- 第一句'));
  assert.match(output, /^第一句话说完了。$/m);
  assert.match(output, /^第三句话同样说完了。$/m);
});

test('regrouping never splits fenced code blocks containing blank lines', () => {
  const input = '```js\nconst a = 1;\n\nconst b = 2;\n```\n\ntail text';
  const output = tidyMarkdown(input);

  assert.match(output, /```js\nconst a = 1;\n\nconst b = 2;\n```/);
});
