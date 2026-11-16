const { chromium } = require('playwright');
const { spawn } = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const net = require('node:net');
const assert = require('node:assert/strict');

(async () => {
  const root = path.resolve(__dirname, '..');
  const binary = process.env.ORIMG_QA_BINARY;
  assert(binary, 'ORIMG_QA_BINARY must point to a locally built test binary');
  const work = await fs.mkdtemp(path.join(os.tmpdir(), 'origin-install-ui-'));
  await fs.symlink(path.join(root, 'static'), path.join(work, 'static'));
  const listener = net.createServer();
  await new Promise((resolve) => listener.listen(0, '127.0.0.1', resolve));
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  const child = spawn(binary, [], { cwd: work, env: { PATH: process.env.PATH, PORT: String(port), DISABLE_BACKGROUND_WORK: 'true' }, stdio: ['ignore', 'pipe', 'pipe'] });
  let output = '';
  child.stdout.on('data', (data) => { output += data; });
  child.stderr.on('data', (data) => { output += data; });
  const stopped = new Promise((resolve) => child.on('exit', resolve));
  let browser;
  try {
    for (let i = 0; i < 100 && !output.includes('listening on'); i++) {
      if (child.exitCode !== null) throw new Error('Isolated server failed to start');
      await new Promise((resolve) => setTimeout(resolve, 100));
    }
    const token = output.match(/setup code: ([a-f0-9]+)/)?.[1];
    assert(token, 'No setup code');
    const base = `http://127.0.0.1:${port}`;
    assert.equal((await fetch(base + '/api/ready')).status, 503);
    assert.equal((await fetch(base + '/api/install/status')).status, 401);
    browser = await chromium.launch({ headless: true, ...(process.env.ORIMG_QA_BROWSER_CHANNEL ? { channel: process.env.ORIMG_QA_BROWSER_CHANNEL } : {}) });
    const screenshots = path.join(root, 'test-results', 'installation');
    await fs.mkdir(screenshots, { recursive: true });
    const errors = [];
    for (const width of [390, 768, 1440, 2560]) {
      const context = await browser.newContext({ viewport: { width, height: 1000 }, colorScheme: 'light' });
      const page = await context.newPage();
      page.on('pageerror', (error) => errors.push(error.message));
      page.on('dialog', (dialog) => dialog.accept());
      let mode = 'fresh';
      let submitted;
      await page.route('**/*', async (route) => {
        const url = new URL(route.request().url());
        assert.equal(url.origin, base, 'Unexpected external network request');
        if (url.pathname === '/api/install/check') {
          return route.fulfill({ json: { bootstrap: mode === 'fresh', resume: mode === 'resume', database: { empty: mode === 'fresh', users: 3, images: 25, version: 9 } } });
        }
        if (url.pathname === '/api/install/complete') {
          submitted = route.request().postDataJSON();
          return route.fulfill({ json: { installed: true, workers_paused: mode !== 'fresh' } });
        }
        return route.continue();
      });
      const layout = async (name) => {
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), `${width}: horizontal overflow at ${name}`);
        const hiddenFields = await page.locator('section[hidden]').evaluateAll((sections) => sections.some((section) => section.getBoundingClientRect().height > 0));
        assert.equal(hiddenFields, false, 'Hidden step occupies layout');
        await page.screenshot({ path: path.join(screenshots, `${width}-${name}.png`), fullPage: true, animations: 'disabled' });
      };
      const openDatabase = async () => {
        await page.goto(base + '/install');
        await page.locator('[name=token]').fill(token);
        await page.getByRole('button', { name: '验证并继续' }).click();
        await page.locator('#databaseStep').waitFor({ state: 'visible' });
        await page.locator('[name=host]').fill('127.0.0.1');
        await page.locator('[name=name]').fill('isolated_mock');
        await page.locator('[name=user]').fill('fixture');
        await page.locator('[name=password]').fill('fixture-only-password');
        await layout('database');
        await page.getByRole('button', { name: '检查连接' }).click();
        await page.locator('#accountStep').waitFor({ state: 'visible' });
      };
      await openDatabase();
      await page.locator('[name=admin_email]').fill('fixture@example.test');
      await page.locator('[name=admin_password]').fill('fixture-owner-password');
      await page.locator('[name=confirm_password]').fill('fixture-owner-password');
      await page.locator('[name=confirmed]').check();
      await layout('fresh');
      await page.getByRole('button', { name: '完成安装', exact: true }).click();
      await page.locator('#successStep').waitFor({ state: 'visible' });
      assert.equal(submitted.existing, false);
      assert.equal(submitted.confirmed, true);
      assert.equal(await page.locator('[name=admin_password]').inputValue(), '');
      await layout('success');
      for (mode of ['existing', 'resume']) {
        await openDatabase();
        assert.equal(await page.locator('#usernameField').isVisible(), false);
        assert.equal(await page.locator('#originalSecretField').isVisible(), mode === 'existing');
        await page.locator('[name=admin_email]').fill('fixture@example.test');
        await page.locator('[name=admin_password]').fill('old-password');
        if (mode === 'existing') await page.locator('[name=original_secret]').fill('0123456789abcdef0123456789abcdef');
        assert.equal(await page.locator('[name=enable_workers]').isChecked(), false);
        await page.locator('[name=confirmed]').check();
        await layout(mode);
        await page.getByRole('button', { name: '连接并升级' }).click();
        await page.locator('#successStep').waitFor({ state: 'visible' });
        assert.equal(submitted.existing, true);
        assert.equal(submitted.enable_workers, false);
      }
      await context.close();
    }
    assert.deepEqual(errors, []);
    console.log('PASS: isolated live startup, protected API, three mocked wizard flows at 390/768/1440/2560px; no external services');
    console.log(`Screenshots: ${screenshots}`);
  } finally {
    if (browser) await browser.close();
    child.kill('SIGTERM');
    await stopped;
  }
})().catch((error) => { console.error(error); process.exitCode = 1; });
