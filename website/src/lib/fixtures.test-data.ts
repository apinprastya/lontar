// Shared test data: a release as GitHub's API returns it.
import type { GitHubRelease } from './releases';

export const asset = (name: string, size = 1000) => ({
  name,
  size,
  browser_download_url: `https://example.test/${name}`,
});

export const sampleRelease: GitHubRelease = {
  tag_name: 'v26.11.2',
  html_url: 'https://github.com/apinprastya/lontar/releases/tag/v26.11.2',
  published_at: '2026-11-10T08:00:00Z',
  assets: [
    asset('lontar-26.11.2-macos-universal.dmg', 60 * 1024 * 1024),
    asset('lontar-26.11.2-windows-amd64-portable.zip'),
    asset('lontar-26.11.2-linux-x86_64.rpm'),
    asset('SHA256SUMS'),
    asset('SHA256SUMS.sig'),
    asset('lontar-26.11.2-linux-amd64.deb'),
    asset('lontar-26.11.2-windows-amd64-setup.exe'),
    asset('lontar-26.11.2-linux-x86_64.AppImage'),
    asset('lontar-26.11.2-linux-x86_64.pkg.tar.zst'),
  ],
};
