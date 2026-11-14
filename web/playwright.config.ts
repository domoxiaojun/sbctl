import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e', timeout: 30_000, fullyParallel: true, workers: 3,
  reporter: [['list']], use: { baseURL: 'http://127.0.0.1:15186', viewport: { width: 1440, height: 1100 }, colorScheme: 'light', reducedMotion: 'reduce', screenshot: 'only-on-failure', trace: 'retain-on-failure' },
  webServer: [
    { command: 'node node_modules/vite/bin/vite.js --host 127.0.0.1 --port 15186', url: 'http://127.0.0.1:15186', reuseExistingServer: false },
    { command: 'node node_modules/vite/bin/vite.js preview --host 127.0.0.1 --port 14186', url: 'http://127.0.0.1:14186', reuseExistingServer: false },
  ],
})
