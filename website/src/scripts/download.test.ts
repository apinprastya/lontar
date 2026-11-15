// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LATEST_API_URL, parseRelease } from '../lib/releases';
import { sampleRelease } from '../lib/fixtures.test-data';
import { init, labelOsButtons, loadRelease, renderRecommended, start } from './download';

const WIN_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)';

function okFetch(body: unknown) {
  return vi.fn(async () => ({ ok: true, json: async () => body }));
}

function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() {
      return m.size;
    },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, v),
  };
}

function downloadPage() {
  document.body.innerHTML = `
    <a data-os-cta href="/download">Download</a>
    <details data-os-section="windows"><summary>Windows</summary></details>
    <details data-os-section="linux"><summary>Linux</summary></details>
    <div id="recommended"><a href="x">fallback</a></div>
    <table><tbody id="downloads-body"><tr><td>fallback</td></tr></tbody></table>
    <a data-release-notes href="x">Release notes</a>
    <a data-checksum="SHA256SUMS" href="x">SHA256SUMS</a>`;
}

beforeEach(() => {
  document.body.innerHTML = '';
  delete document.documentElement.dataset.release;
});

describe('loadRelease', () => {
  it('fetches the latest release and caches it', async () => {
    const storage = memoryStorage();
    const fetchFn = okFetch(sampleRelease);
    const info = await loadRelease(fetchFn, storage);
    expect(info?.version).toBe('26.11.2');
    expect(fetchFn).toHaveBeenCalledWith(LATEST_API_URL, expect.anything());

    const again = await loadRelease(fetchFn, storage);
    expect(again?.version).toBe('26.11.2');
    expect(fetchFn).toHaveBeenCalledTimes(1);
  });

  it('returns null on HTTP errors, network errors and unusable bodies', async () => {
    expect(await loadRelease(vi.fn(async () => ({ ok: false, json: async () => ({}) })), null)).toBeNull();
    expect(await loadRelease(vi.fn(async () => Promise.reject(new Error('offline'))), null)).toBeNull();
    expect(await loadRelease(okFetch({ message: 'Not Found' }), null)).toBeNull();
  });

  it('survives broken or blocked storage', async () => {
    const broken = memoryStorage();
    broken.setItem('lontar-latest-release', '{not json');
    broken.setItem = () => {
      throw new Error('quota');
    };
    const info = await loadRelease(okFetch(sampleRelease), broken);
    expect(info?.version).toBe('26.11.2');
  });
});

describe('init', () => {
  it('labels buttons, renders downloads and links for the visitor OS', async () => {
    downloadPage();
    const info = await init({
      doc: document,
      userAgent: WIN_UA,
      platform: '',
      fetchFn: okFetch(sampleRelease),
      storage: null,
    });
    expect(info).not.toBeNull();
    expect(document.documentElement.dataset.release).toBe('ready');
    expect(document.querySelector('[data-os-cta]')?.textContent).toBe('Download for Windows');
    expect((document.querySelector('[data-os-section="windows"]') as HTMLDetailsElement).open).toBe(true);
    expect((document.querySelector('[data-os-section="linux"]') as HTMLDetailsElement).open).toBe(false);

    const primary = document.querySelector('#recommended a') as HTMLAnchorElement;
    expect(primary.textContent).toBe('Download for Windows');
    expect(primary.href).toContain('windows-amd64-setup.exe');

    const rows = document.querySelectorAll('#downloads-body tr');
    expect(rows).toHaveLength(6);
    expect(document.querySelectorAll('#downloads-body tr.is-yours')).toHaveLength(2);

    expect((document.querySelector('[data-release-notes]') as HTMLAnchorElement).href).toBe(sampleRelease.html_url);
    expect((document.querySelector('[data-checksum]') as HTMLAnchorElement).href).toContain('/SHA256SUMS');
  });

  it('keeps the fallback links when the API is unavailable', async () => {
    downloadPage();
    const info = await init({
      doc: document,
      userAgent: WIN_UA,
      platform: '',
      fetchFn: vi.fn(async () => ({ ok: false, json: async () => ({}) })),
      storage: null,
    });
    expect(info).toBeNull();
    expect(document.documentElement.dataset.release).toBe('unavailable');
    expect(document.querySelector('#recommended a')?.textContent).toBe('fallback');
  });

  it('does not fetch on pages without download slots', async () => {
    document.body.innerHTML = '<a data-os-cta href="/download">Download</a>';
    const fetchFn = okFetch(sampleRelease);
    await init({ doc: document, userAgent: WIN_UA, platform: '', fetchFn, storage: null });
    expect(fetchFn).not.toHaveBeenCalled();
    expect(document.documentElement.dataset.release).toBeUndefined();
  });
});

describe('renderRecommended', () => {
  it('asks the visitor to choose when their OS is unknown', () => {
    const el = document.createElement('div');
    renderRecommended(el, parseRelease(sampleRelease)!, null);
    expect(el.textContent).toContain('Pick the file for your computer below');
    expect(el.querySelector('a')).toBeNull();
  });

  it('escapes file names', () => {
    const el = document.createElement('div');
    const info = parseRelease({
      ...sampleRelease,
      assets: [{ name: 'x<b>-macos-universal.dmg', size: 0, browser_download_url: 'https://e.test/"a' }],
    })!;
    renderRecommended(el, info, 'macos');
    expect(el.querySelector('b')).toBeNull();
    expect(el.querySelector('code')?.textContent).toBe('x<b>-macos-universal.dmg');
    expect(el.textContent).not.toContain(', 0');
  });
});

describe('labelOsButtons', () => {
  it('leaves buttons alone for unknown systems', () => {
    document.body.innerHTML = '<a data-os-cta>Download</a>';
    labelOsButtons(document, null);
    expect(document.querySelector('a')?.textContent).toBe('Download');
  });
});

describe('start', () => {
  it('wires the real browser globals', async () => {
    downloadPage();
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(sampleRelease), { status: 200 }),
    );
    const info = await start();
    expect(info?.version).toBe('26.11.2');
    expect(fetchSpy).toHaveBeenCalled();
    fetchSpy.mockRestore();
  });
});
