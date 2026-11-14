import { defineConfig, devices } from '@playwright/test'

// End-to-end harness (design §7.7, §7.10). Phase 0 runs against the MSW mock
// build served by `vite preview`; later phases add a project that points
// E2E_BASE_URL at dismistd plus tools/fakeap. Every spec runs at the four
// widths (390×844, 768×1024, 1024×768, 1440×900), plus touch runs at 768
// (iPad portrait) and 1024 (iPad landscape), so the 16 px rule is checked
// under `pointer: coarse` too.

const port = Number(process.env.E2E_PORT ?? 4319)
const external = process.env.E2E_BASE_URL

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: external ?? `http://127.0.0.1:${port}`,
    trace: 'retain-on-failure',
    serviceWorkers: 'allow',
  },
  projects: [
    { name: 'phone-390', use: { ...devices['Desktop Chrome'], viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, deviceScaleFactor: 3 } },
    { name: 'tablet-768', use: { ...devices['Desktop Chrome'], viewport: { width: 768, height: 1024 } } },
    { name: 'tablet-768-touch', use: { ...devices['Desktop Chrome'], viewport: { width: 768, height: 1024 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 } },
    { name: 'laptop-1024', use: { ...devices['Desktop Chrome'], viewport: { width: 1024, height: 768 } } },
    { name: 'tablet-1024-touch', use: { ...devices['Desktop Chrome'], viewport: { width: 1024, height: 768 }, isMobile: true, hasTouch: true, deviceScaleFactor: 2 } },
    { name: 'desktop-1440', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } },
  ],
  webServer: external
    ? undefined
    : {
        command: `npm run build:mock && npm run preview:mock -- --port ${port} --strictPort --host 127.0.0.1`,
        url: `http://127.0.0.1:${port}/next/login`,
        reuseExistingServer: !process.env.CI,
        timeout: 180_000,
      },
})
