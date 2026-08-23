import { test } from 'node:test';
import assert from 'node:assert/strict';

import { parsePage } from '../src/reader.js';
import { ReaderError } from '../src/errors.js';

const BASE = 'https://example.com/posts/vector-db';

const ARTICLE_HTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <title>Understanding Vector Databases - Example Engineering Blog</title>
  <meta name="description" content="A practical introduction to vector databases.">
  <meta property="og:site_name" content="Example Engineering">
  <meta property="article:published_time" content="2026-08-01T10:00:00Z">
  <style>.body { color: red; }</style>
  <script>window.tracker = 1;</script>
</head>
<body>
  <header>
    <nav><a href="/">Home</a> <a href="/posts">Posts</a> <a href="/about">About</a></nav>
  </header>
  <div class="sidebar share-widget"><a href="/share">Share this article</a></div>
  <main>
    <article>
      <h1>Understanding Vector Databases</h1>
      <p>Vector databases store high-dimensional embeddings and support fast
         nearest-neighbor search. They have become a core building block for
         retrieval augmented generation pipelines in modern machine learning
         systems, and choosing one wisely matters a great deal.</p>
      <p>This article walks through the core concepts step by step. See the
         <a href="/docs/intro">introduction guide</a> for more details.</p>
      <img src="images/architecture.png" alt="Architecture diagram">
      <img src="https://analytics.example/pixel.gif" width="1" height="1">
      <img src="data:image/png;base64,AAAA" alt="spacer">
      <pre><code>def search(query):
    return index.query(query, top_k=10)</code></pre>
      <ul>
        <li>Embedding ingestion</li>
        <li>Approximate search</li>
      </ul>
      <table>
        <tr><th>Engine</th><th>Index</th></tr>
        <tr><td>Qdrant</td><td>HNSW</td></tr>
      </table>
    </article>
  </main>
  <footer>© Example Corp 2026. All rights reserved.</footer>
  <div class="related-posts"><a href="/posts/other">Read next: Sharding strategies</a></div>
</body>
</html>`;

const OPTIONS = { format: 'markdown' as const, withLinks: true, withImages: true };

test('extracts the article with metadata and clean markdown', () => {
  const result = parsePage(ARTICLE_HTML, BASE, OPTIONS);

  assert.equal(result.extraction, 'readability');
  // Readability keeps the full <title> and de-duplicates the matching h1
  // from the content (same behavior as jina-ai/reader).
  assert.equal(result.title, 'Understanding Vector Databases - Example Engineering Blog');
  assert.equal(result.description, 'A practical introduction to vector databases.');
  assert.equal(result.siteName, 'Example Engineering');
  assert.equal(result.publishedTime, '2026-08-01T10:00:00Z');
  assert.equal(result.lang, 'en');
  assert.ok(result.wordCount > 10);

  const content = result.content;
  assert.ok(!content.includes('# Understanding Vector Databases'));
  assert.match(content, /nearest-neighbor search/);
  assert.match(content, /\[introduction guide\]\(https:\/\/example\.com\/docs\/intro\)/);
  assert.match(content, /!\[Architecture diagram\]\(https:\/\/example\.com\/posts\/images\/architecture\.png\)/);
  assert.match(content, /```/);
  assert.match(content, /def search\(query\):/);
  assert.match(content, /- Embedding ingestion/);
  assert.match(content, /Qdrant/);

  // Noise is stripped.
  assert.ok(!content.includes('Home'));
  assert.ok(!content.includes('About'));
  assert.ok(!content.includes('Share this article'));
  assert.ok(!content.includes('Example Corp'));
  assert.ok(!content.includes('Sharding strategies'));
  assert.ok(!content.includes('analytics.example'));
  assert.ok(!content.includes('data:image'));
  assert.ok(!content.includes('window.tracker'));
});

test('text format returns plain text without markdown markers', () => {
  const result = parsePage(ARTICLE_HTML, BASE, { ...OPTIONS, format: 'text' });

  assert.ok(result.content.includes('nearest-neighbor search'));
  assert.ok(!result.content.includes('#'));
  assert.ok(!result.content.includes('['));
});

test('html format returns the extracted html fragment', () => {
  const result = parsePage(ARTICLE_HTML, BASE, { ...OPTIONS, format: 'html' });

  assert.match(result.content, /<p>/);
  assert.ok(!result.content.includes('<script'));
});

test('falls back to the cleaned body when readability finds nothing', () => {
  // Readability returns null when the body has no paragraph-ish candidate
  // elements (only a heading); the pre-cleaned body still carries enough
  // text to pass our own quality gate.
  const html = `<!DOCTYPE html>
<html lang="en">
<head><title>Landing</title></head>
<body>
  <nav><a href="/">Home</a></nav>
  <h1>This page is a simple landing page with a long heading text over sixty chars</h1>
  <footer>© Example Corp 2026</footer>
</body>
</html>`;

  const result = parsePage(html, 'https://example.com/', OPTIONS);

  assert.equal(result.extraction, 'fallback-body');
  assert.match(result.content, /long heading text/);
  assert.ok(!result.content.includes('Example Corp'));
  assert.ok(!result.content.includes('Home'));
});

test('rejects pages with no extractable content', () => {
  const html = `<html><body><p>hi</p></body></html>`;

  assert.throws(
    () => parsePage(html, 'https://example.com/', OPTIONS),
    (err: unknown) => err instanceof ReaderError && err.code === 'parse_failed',
  );
});

test('removes embedded video player chrome from the article', () => {
  // Mirrors the qq.com thumbplayer markup: a videoPlayer wrapper holding
  // the poster image, control UI text and a float-window tooltip.
  const html = `<!DOCTYPE html>
<html lang="zh">
<head><title>示例新闻</title></head>
<body>
  <article>
    <h1>示例新闻标题文字长度超过阈值以保证内容提取</h1>
    <div class="videoPlayer" id="vid1_wrap">
      <div class="_mod_thumbplayer_container_" data-tp="abc">
        <img class="txp_poster_img" src="http://cdn.example.com/poster.jpg">
        <div class="txp_videos_container"><video src="blob:https://example.com/x"></video></div>
        <div class="plugin_ctrl_txp_bottom">
          <div aria-label="暂停/播放"><p>暂停</p></div>
          <div><p>00:02</p><p>/</p><p>06:04</p></div>
          <div aria-label="倍速"><p>倍速</p></div>
        </div>
        <div class="custom-loading hide"><img src="http://cdn.example.com/loading.gif"></div>
      </div>
      <div class="player-float-head"><h4 class="float-title">按住画面移动小窗</h4></div>
    </div>
    <p data-source="cke">正文第一段，内容足够长以通过最小内容阈值校验，并且以句号结尾。</p>
    <p data-source="cke">正文第二段，内容足够长以通过最小内容阈值校验，并且以句号结尾。</p>
  </article>
</body>
</html>`;

  const result = parsePage(html, 'https://example.com/news/1', OPTIONS);

  assert.ok(!result.content.includes('暂停'));
  assert.ok(!result.content.includes('06:04'));
  assert.ok(!result.content.includes('倍速'));
  assert.ok(!result.content.includes('按住画面'));
  assert.ok(!result.content.includes('poster.jpg'));
  assert.ok(!result.content.includes('loading.gif'));
  assert.ok(result.content.includes('正文第一段'));
  assert.ok(result.content.includes('正文第二段'));
});
