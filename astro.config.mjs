import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

const ALLOWED_HOSTS = [
  'localhost',
  '127.0.0.1',
  'dev.ctl.qzz.io',
  ...(process.env.ORVA_ALLOWED_HOSTS?.split(',')
    .map((h) => h.trim())
    .filter(Boolean) ?? []),
];

export default defineConfig({
  site: 'https://harsh-2002.github.io',
  base: '/Orva',
  output: 'static',
  compressHTML: true,

  // Vite rejects requests whose Host header it does not recognise, so a reverse
  // proxy in front of `astro dev` gets a 403 and the proxy reports 502.
  //
  // This covers `astro dev` only. For a static build `astro preview` runs
  // Astro's own server rather than Vite's, ignores this entirely, and takes
  // `--allowed-hosts` on the command line instead. See `npm run serve`.
  //
  // Neither affects the deployed site: /dist is static files with no host
  // checking at all.
  vite: {
    server: { allowedHosts: ALLOWED_HOSTS },
  },

  trailingSlash: 'always',
  integrations: [
    sitemap({
      changefreq: 'weekly',
      priority: 0.8,
      // /og/ exists only to be screenshotted into public/og.png. It is a
      // rendering surface, not a page anyone should land on.
      filter: (page) => !page.includes('/og/'),
    }),
  ],
});
