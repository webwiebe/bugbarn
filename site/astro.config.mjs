import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';

export default defineConfig({
  site: 'https://webwiebe.github.io',
  base: '/bugbarn',
  integrations: [tailwind()],
  output: 'static',
});
