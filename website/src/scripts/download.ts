// Browser glue for the Download page and the OS-aware download buttons.
// The page ships with working fallback links (to GitHub's "latest release"
// page); this script replaces them with direct file links when the GitHub API
// answers. Dependencies are injected so tests can run it against jsdom.

import {
  LATEST_API_URL,
  OS_NAMES,
  detectOS,
  formatDate,
  formatSize,
  parseRelease,
  primaryFor,
  type OS,
  type ReleaseInfo,
} from '../lib/releases';

const CACHE_KEY = 'lontar-latest-release';

type FetchFn = (url: string, init?: RequestInit) => Promise<Pick<Response, 'ok' | 'json'>>;

function readCache(storage: Storage | null): unknown {
  try {
    const raw = storage?.getItem(CACHE_KEY);
    return raw ? JSON.parse(raw) : null;
  } catch {
    return null;
  }
}

function writeCache(storage: Storage | null, data: unknown): void {
  try {
    storage?.setItem(CACHE_KEY, JSON.stringify(data));
  } catch {
    // Storage full or blocked: the next visit fetches again.
  }
}

/** Latest stable release, cached for the browser session; null on any failure. */
export async function loadRelease(fetchFn: FetchFn, storage: Storage | null): Promise<ReleaseInfo | null> {
  const cached = parseRelease(readCache(storage));
  if (cached) return cached;
  try {
    const res = await fetchFn(LATEST_API_URL, { headers: { Accept: 'application/vnd.github+json' } });
    if (!res.ok) return null;
    const data: unknown = await res.json();
    const info = parseRelease(data);
    if (info) writeCache(storage, data);
    return info;
  } catch {
    return null;
  }
}

function esc(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

/** Fills the "recommended for you" box. */
export function renderRecommended(el: HTMLElement, info: ReleaseInfo, os: OS | null): void {
  const primary = primaryFor(info, os);
  const meta = [`Version ${info.version}`, formatDate(info.publishedAt)].filter(Boolean).join(', ');
  if (!primary) {
    el.innerHTML = `
      <p class="rec-title">Lontar ${esc(info.version)}</p>
      <p class="rec-meta">${esc(meta)}. Pick the file for your computer below.</p>`;
    return;
  }
  const size = formatSize(primary.size);
  el.innerHTML = `
    <a class="button button-primary button-large" href="${esc(primary.url)}">Download for ${OS_NAMES[primary.os]}</a>
    <p class="rec-meta">${esc(primary.label)}${size ? `, ${esc(size)}` : ''}. ${esc(meta)}.</p>
    <p class="rec-file"><code>${esc(primary.name)}</code></p>`;
}

/** Fills the table of every platform's files. */
export function renderTable(tbody: HTMLElement, info: ReleaseInfo, os: OS | null): void {
  tbody.innerHTML = info.downloads
    .map(
      (d) => `
      <tr${d.os === os ? ' class="is-yours"' : ''}>
        <th scope="row">${OS_NAMES[d.os]}</th>
        <td><a href="${esc(d.url)}">${esc(d.label)}</a><span class="file">${esc(d.name)}</span></td>
        <td>${esc(d.note)}</td>
        <td class="size">${esc(formatSize(d.size))}</td>
      </tr>`,
    )
    .join('');
}

/** Points "SHA256SUMS" and "release notes" links at the actual release. */
export function renderLinks(doc: Document, info: ReleaseInfo): void {
  doc.querySelectorAll<HTMLAnchorElement>('[data-release-notes]').forEach((a) => {
    a.href = info.url;
    a.textContent = `Release notes for ${info.version}`;
  });
  for (const c of info.checksums) {
    doc.querySelectorAll<HTMLAnchorElement>(`[data-checksum="${c.name}"]`).forEach((a) => {
      a.href = c.url;
    });
  }
}

/** Labels every [data-os-cta] button with the visitor's OS. */
export function labelOsButtons(doc: Document, os: OS | null): void {
  if (!os) return;
  doc.querySelectorAll<HTMLElement>('[data-os-cta]').forEach((el) => {
    el.textContent = `Download for ${OS_NAMES[os]}`;
  });
  doc.querySelectorAll<HTMLElement>(`[data-os-section="${os}"]`).forEach((el) => {
    el.classList.add('is-yours');
    if (el instanceof HTMLDetailsElement) el.open = true;
  });
}

export interface Env {
  doc: Document;
  userAgent: string;
  platform: string;
  fetchFn: FetchFn;
  storage: Storage | null;
}

/** Runs on every page: OS-aware labels everywhere, release data where wanted. */
export async function init(env: Env): Promise<ReleaseInfo | null> {
  const { doc } = env;
  const os = detectOS(env.userAgent, env.platform);
  labelOsButtons(doc, os);

  const rec = doc.getElementById('recommended');
  const tbody = doc.getElementById('downloads-body');
  if (!rec && !tbody) return null;

  doc.documentElement.dataset.release = 'loading';
  const info = await loadRelease(env.fetchFn, env.storage);
  if (!info) {
    doc.documentElement.dataset.release = 'unavailable';
    return null;
  }
  if (rec) renderRecommended(rec, info, os);
  if (tbody) renderTable(tbody, info, os);
  renderLinks(doc, info);
  doc.documentElement.dataset.release = 'ready';
  return info;
}

function sessionStore(): Storage | null {
  try {
    return window.sessionStorage;
  } catch {
    return null;
  }
}

/** Entry point used by the pages. */
export function start(): Promise<ReleaseInfo | null> {
  const nav = navigator as Navigator & { userAgentData?: { platform?: string } };
  return init({
    doc: document,
    userAgent: nav.userAgent,
    platform: nav.userAgentData?.platform ?? nav.platform ?? '',
    fetchFn: (url, opts) => fetch(url, opts),
    storage: sessionStore(),
  });
}
