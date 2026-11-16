// All APIs are isolated fixtures. Never connects to a database.
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '../static');
const out = '/tmp/orimg-responsive-qa';
fs.mkdirSync(out, { recursive: true });
(async () => {
  const server = http.createServer((req, res) => {
    const file = path.resolve(root, '.' + new URL(req.url, 'http://localhost').pathname);
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) { res.writeHead(404); res.end(); return; }
    res.setHeader('Content-Type', ({ '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
    fs.createReadStream(file).pipe(res);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const context = await browser.newContext();
    await context.addInitScript(() => localStorage.setItem('zhuapic_token', 'fixture-only'));
    const settings = { site_name: 'Origin', hero_title: 'Origin 0.3.0-beta.10', hero_subtitle: '', footer_text: 'Powered by Yuki.', s3_configured: 'true', storage_mode: 's3', admin_path: '/admin.html', user_path: '/dashboard.html' };
    const user = { id: 1, username: 'Preview', email: 'preview@example.test', role: 'admin', active: true, group_id: 1 };
    const group = { id: 1, name: 'Member', slug: 'member', policy: { id: 1, max_file_mb: 200, max_batch_count: 100, active: true } };
    let writes = [], gate = null, fail = false;
    await context.route('**/api/**', async route => {
      const req = route.request(), pathname = new URL(req.url()).pathname;
      if (req.method() !== 'GET') {
        writes.push({ pathname, payload: req.postDataJSON() });
        if (gate) await gate;
        return route.fulfill({ status: fail ? 500 : 200, json: fail ? { error: '模拟保存失败' } : {} });
      }
      const data = pathname.endsWith('/public') ? settings : pathname.endsWith('/settings') ? { settings } : pathname === '/api/me' ? { user, upload_policy: group.policy } : pathname === '/api/admin/groups' ? { groups: [group] } : pathname === '/api/admin/users' ? { users: [user] } : {};
      return route.fulfill({ json: data });
    });
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', err => errors.push(err.message));
    for (const [width, height] of [[390,844],[768,1024],[1366,768],[1920,1080],[2560,1440],[2940,1512],[3440,1440],[3840,2160],[1440,600]]) {
      await page.setViewportSize({ width, height });
      await page.goto(`${origin}/index.html`);
      await page.waitForFunction(() => document.body.dataset.siteState === 'ready');
      await page.evaluate(() => document.documentElement.dataset.theme = 'dark');
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${width}: page overflow`);
      const drop = await page.locator('#dropzone').boundingBox();
      const header = await page.locator('.topbar').boundingBox();
      assert(drop.x >= 0 && drop.x + drop.width <= width);
      assert(drop.y >= header.y + header.height);
      if (width >= 1920) assert(drop.width / width >= .42, `${width}: upload area too narrow`);
      if (height >= 1080) assert(drop.height >= 460, `${width}: upload area too short`);
      if ([390,1920,2560,2940,3840].includes(width)) await page.screenshot({ path: `${out}/upload-${width}.png`, fullPage: true, animations: 'disabled' });
    }
    await page.setViewportSize({ width: 2560, height: 1440 });
    await page.goto(`${origin}/admin.html#site`);
    await page.locator('#siteForm').waitFor();
    const targets = [
      { panel: 'site', form: '#siteForm', requests: 1 },
      { panel: 'storage', form: '#storageForm', requests: 1 },
      { panel: 'security', form: '#securityForm', requests: 1 },
      { panel: 'groups', form: '.policy-card', requests: 2 },
      { panel: 'users', form: '#userEditorForm', requests: 1 },
    ];
    for (const target of targets) {
      await page.locator(`.admin-nav [data-panel="${target.panel}"]`).click();
      if (target.panel === 'storage') await page.locator('.storage-runtime-settings summary').click();
      if (target.panel === 'groups') await page.locator('.group-entry > summary').first().click();
      if (target.panel === 'users') await page.locator('[data-edit-user]').first().click();
      const form = page.locator(target.form).first();
      const button = form.locator('button[type="submit"]');
      const before = writes.length;
      page.once('dialog', dialog => dialog.dismiss());
      await button.click();
      assert.equal(writes.length, before, `${target.panel}: cancelled save must not write`);
      let release;
      gate = new Promise(resolve => { release = resolve; });
      page.once('dialog', dialog => dialog.accept());
      await button.click();
      await page.waitForFunction(selector => document.querySelector(selector).getAttribute('aria-busy') === 'true', target.form);
      assert.equal(await button.isDisabled(), true);
      assert.equal(await button.textContent(), '保存中…');
      await form.evaluate(el => { for (let i = 0; i < 5; i++) el.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
      assert.equal(writes.length, before + 1, `${target.panel}: duplicate submit`);
      release(); gate = null;
      await page.waitForFunction(() => document.getElementById('saveFeedback').textContent.includes('已保存'));
      assert.equal(writes.length, before + target.requests);
    }
    await page.locator('.admin-nav [data-panel="site"]').click();
    fail = true;
    page.once('dialog', dialog => dialog.accept());
    await page.locator('#siteForm button[type="submit"]').click();
    await page.waitForFunction(() => document.getElementById('saveFeedback').textContent === '模拟保存失败');
    assert.equal(await page.locator('#siteForm button[type="submit"]').isDisabled(), false);
    fail = false;
    page.once('dialog', dialog => dialog.accept());
    await page.locator('#siteForm button[type="submit"]').click();
    await page.waitForFunction(() => document.getElementById('saveFeedback').textContent === '页面设置已保存');
    await page.screenshot({ path: `${out}/admin-save-2560.png`, animations: 'disabled' });
    for (const width of [390,1366,2560,3840]) {
      await page.setViewportSize({ width, height: width < 1000 ? 844 : 1440 });
      await page.goto(`${origin}/dashboard.html`);
      await page.locator('#dashboardShell').waitFor();
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `${width}: dashboard overflow`);
    }
    assert.deepEqual(errors, []);
    await context.close();
    console.log('Responsive layouts: 390 to 3840 CSS px, short windows, dashboard; saves: confirmation/cancel, deduplication, failure/retry passed.');
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(err => { console.error(err); process.exitCode = 1; });
