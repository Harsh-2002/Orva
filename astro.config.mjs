import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

export default defineConfig({
  site: 'https://harsh-2002.github.io',
  base: '/Orva',
  output: 'static',
  compressHTML: true,
  trailingSlash: 'always',
  integrations: [
    sitemap({
      changefreq: 'weekly',
      priority: 0.8,
    }),
  ],
});
