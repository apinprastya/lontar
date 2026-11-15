import { describe, expect, it } from 'vitest';
import {
  LATEST_RELEASE_URL,
  classifyAsset,
  detectOS,
  formatDate,
  formatSize,
  parseRelease,
  primaryFor,
} from './releases';
import { asset, sampleRelease } from './fixtures.test-data';

describe('detectOS', () => {
  it.each([
    ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)', '', 'windows'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5)', '', 'macos'],
    ['Mozilla/5.0 (X11; Linux x86_64)', '', 'linux'],
    ['Mozilla/5.0 (X11; Ubuntu)', '', 'linux'],
    ['Mozilla/5.0', 'Win32', 'windows'],
    ['Mozilla/5.0', 'MacIntel', 'macos'],
    ['Mozilla/5.0', 'Linux x86_64', 'linux'],
  ])('%s / %s → %s', (ua, pf, want) => {
    expect(detectOS(ua, pf)).toBe(want);
  });

  it('returns null for phones and unknown systems', () => {
    expect(detectOS('Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)')).toBeNull();
    expect(detectOS('Mozilla/5.0 (Linux; Android 14) Mobile')).toBeNull();
    expect(detectOS('Mozilla/5.0 (FreeBSD)')).toBeNull();
  });
});

describe('classifyAsset', () => {
  it('maps every published file name', () => {
    const kinds = sampleRelease.assets.map((a) => classifyAsset(a)?.kind ?? null);
    expect(kinds).toEqual(['dmg', 'portable', 'rpm', null, null, 'deb', 'setup', 'appimage', null]);
  });

  it('keeps name, size and url', () => {
    expect(classifyAsset(asset('lontar-1-macos-universal.dmg', 42))).toMatchObject({
      os: 'macos',
      name: 'lontar-1-macos-universal.dmg',
      size: 42,
      url: 'https://example.test/lontar-1-macos-universal.dmg',
    });
  });
});

describe('parseRelease', () => {
  it('orders downloads and collects checksums', () => {
    const info = parseRelease(sampleRelease)!;
    expect(info.version).toBe('26.11.2');
    expect(info.url).toBe(sampleRelease.html_url);
    expect(info.publishedAt).toBe('2026-11-10T08:00:00Z');
    expect(info.downloads.map((d) => d.kind)).toEqual(['appimage', 'deb', 'rpm', 'setup', 'portable', 'dmg']);
    expect(info.checksums.map((c) => c.name)).toEqual(['SHA256SUMS', 'SHA256SUMS.sig']);
  });

  it('rejects junk, drafts, pre-releases and releases without packages', () => {
    expect(parseRelease(null)).toBeNull();
    expect(parseRelease('nope')).toBeNull();
    expect(parseRelease({ message: 'Not Found' })).toBeNull();
    expect(parseRelease({ ...sampleRelease, draft: true })).toBeNull();
    expect(parseRelease({ ...sampleRelease, prerelease: true })).toBeNull();
    expect(parseRelease({ ...sampleRelease, assets: [asset('SHA256SUMS')] })).toBeNull();
  });

  it('falls back to the latest-release page and no date', () => {
    const { html_url: _u, published_at: _p, ...rest } = sampleRelease;
    const info = parseRelease(rest)!;
    expect(info.url).toBe(LATEST_RELEASE_URL);
    expect(info.publishedAt).toBeNull();
  });
});

describe('primaryFor', () => {
  const info = parseRelease(sampleRelease)!;
  it('prefers the first listed file for the OS', () => {
    expect(primaryFor(info, 'linux')?.kind).toBe('appimage');
    expect(primaryFor(info, 'windows')?.kind).toBe('setup');
    expect(primaryFor(info, 'macos')?.kind).toBe('dmg');
  });
  it('returns null without an OS or a matching file', () => {
    expect(primaryFor(info, null)).toBeNull();
    expect(primaryFor({ ...info, downloads: [] }, 'linux')).toBeNull();
  });
});

describe('formatSize', () => {
  it.each([
    [0, ''],
    [Number.NaN, ''],
    [512, '512 B'],
    [1536, '1.5 KB'],
    [60 * 1024 * 1024, '60 MB'],
    [3 * 1024 ** 4, '3072 GB'],
  ])('%d → %s', (n, want) => {
    expect(formatSize(n)).toBe(want);
  });
});

describe('formatDate', () => {
  it('formats ISO dates in UTC and ignores bad input', () => {
    expect(formatDate('2026-11-10T08:00:00Z')).toBe('November 10, 2026');
    expect(formatDate(null)).toBe('');
    expect(formatDate('not a date')).toBe('');
  });
});
