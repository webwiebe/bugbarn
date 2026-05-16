import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';

export default defineConfig({
  site: 'https://bugbarn.wiebe.xyz',
  base: '/',
  integrations: [tailwind()],
  output: 'static',
});
