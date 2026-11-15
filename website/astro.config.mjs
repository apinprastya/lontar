import { defineConfig } from 'astro/config';

// Static marketing site for Lontar. `npm run build` writes plain HTML/CSS/JS
// to dist/, which any static host (Cloudflare Pages, Netlify, Vercel) serves.
export default defineConfig({
  output: 'static',
  // Set SITE_URL in the host's build settings so canonical/OG URLs are absolute.
  site: process.env.SITE_URL || undefined,
  trailingSlash: 'ignore',
});
