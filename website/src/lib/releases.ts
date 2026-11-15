// Pure release logic for the Download page: which OS the visitor is on, which
// release asset belongs to which platform, and how to show it. No DOM access
// here, so it is unit-tested directly.

export const REPO = 'apinprastya/lontar';
export const RELEASES_URL = `https://github.com/${REPO}/releases`;
export const LATEST_RELEASE_URL = `${RELEASES_URL}/latest`;
// Same endpoint the app's own updater uses: the newest stable release
// (drafts and pre-releases are never returned).
export const LATEST_API_URL = `https://api.github.com/repos/${REPO}/releases/latest`;

export type OS = 'linux' | 'windows' | 'macos';

export interface GitHubAsset {
  name: string;
  size: number;
  browser_download_url: string;
}

export interface GitHubRelease {
  tag_name: string;
  html_url: string;
  published_at: string | null;
  draft?: boolean;
  prerelease?: boolean;
  assets: GitHubAsset[];
}

export interface Download {
  os: OS;
  /** Stable key, also the order downloads are listed in. */
  kind: 'appimage' | 'deb' | 'rpm' | 'setup' | 'portable' | 'dmg';
  label: string;
  note: string;
  name: string;
  size: number;
  url: string;
}

export interface ReleaseInfo {
  version: string;
  url: string;
  publishedAt: string | null;
  downloads: Download[];
  checksums: { name: string; url: string }[];
}

interface Pattern {
  re: RegExp;
  os: OS;
  kind: Download['kind'];
  label: string;
  note: string;
}

// File names follow README "Installing": lontar-<version>-<platform>.<ext>.
const PATTERNS: Pattern[] = [
  { re: /-linux-x86_64\.AppImage$/i, os: 'linux', kind: 'appimage', label: 'AppImage', note: 'Any Linux distribution, x86_64' },
  { re: /-linux-amd64\.deb$/i, os: 'linux', kind: 'deb', label: '.deb package', note: 'Debian 13+, Ubuntu 24.04+' },
  { re: /-linux-x86_64\.rpm$/i, os: 'linux', kind: 'rpm', label: '.rpm package', note: 'Fedora 39+' },
  { re: /-windows-amd64-setup\.exe$/i, os: 'windows', kind: 'setup', label: 'Installer', note: 'Windows 10/11, x64' },
  { re: /-windows-amd64-portable\.zip$/i, os: 'windows', kind: 'portable', label: 'Portable .zip', note: 'No install, needs WebView2' },
  { re: /-macos-universal\.dmg$/i, os: 'macos', kind: 'dmg', label: 'Disk image', note: 'macOS 12+, Apple silicon and Intel' },
];

const KIND_ORDER = PATTERNS.map((p) => p.kind);

export const OS_NAMES: Record<OS, string> = {
  linux: 'Linux',
  windows: 'Windows',
  macos: 'macOS',
};

/** Detects the visitor's desktop OS; null for phones, tablets and unknowns. */
export function detectOS(userAgent: string, platform = ''): OS | null {
  const ua = userAgent.toLowerCase();
  const pf = platform.toLowerCase();
  if (/android|iphone|ipad|ipod|mobile/.test(ua)) return null;
  if (pf.startsWith('win') || ua.includes('windows')) return 'windows';
  if (pf.startsWith('mac') || ua.includes('mac os x') || ua.includes('macintosh')) return 'macos';
  if (pf.includes('linux') || ua.includes('linux') || ua.includes('x11')) return 'linux';
  return null;
}

/** Maps a release asset to a platform download, or null if it is not one. */
export function classifyAsset(asset: GitHubAsset): Download | null {
  const p = PATTERNS.find((pt) => pt.re.test(asset.name));
  if (!p) return null;
  return {
    os: p.os,
    kind: p.kind,
    label: p.label,
    note: p.note,
    name: asset.name,
    size: asset.size,
    url: asset.browser_download_url,
  };
}

/** Turns a GitHub release into what the page shows; null if unusable. */
export function parseRelease(data: unknown): ReleaseInfo | null {
  if (!data || typeof data !== 'object') return null;
  const r = data as Partial<GitHubRelease>;
  if (typeof r.tag_name !== 'string' || !Array.isArray(r.assets)) return null;
  if (r.draft || r.prerelease) return null;
  const downloads = r.assets
    .map(classifyAsset)
    .filter((d): d is Download => d !== null)
    .sort((a, b) => KIND_ORDER.indexOf(a.kind) - KIND_ORDER.indexOf(b.kind));
  if (downloads.length === 0) return null;
  const checksums = r.assets
    .filter((a) => a.name === 'SHA256SUMS' || a.name === 'SHA256SUMS.sig')
    .map((a) => ({ name: a.name, url: a.browser_download_url }));
  return {
    version: r.tag_name.replace(/^v/, ''),
    url: typeof r.html_url === 'string' ? r.html_url : LATEST_RELEASE_URL,
    publishedAt: r.published_at ?? null,
    downloads,
    checksums,
  };
}

/** The download to recommend first for an OS (installer before portable). */
export function primaryFor(info: ReleaseInfo, os: OS | null): Download | null {
  if (!os) return null;
  return info.downloads.find((d) => d.os === os) ?? null;
}

export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '';
  const units = ['B', 'KB', 'MB', 'GB'];
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${i === 0 ? n : n.toFixed(n < 10 ? 1 : 0)} ${units[i]}`;
}

export function formatDate(iso: string | null): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString('en', { year: 'numeric', month: 'long', day: 'numeric', timeZone: 'UTC' });
}
