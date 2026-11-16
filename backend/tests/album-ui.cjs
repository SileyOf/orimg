// Isolated browser regression: every API request is mocked; no database is used.
const fs = require('node:fs');
const path = require('node:path');
const http = require('node:http');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const sharp = require('sharp');
const root = path.resolve(__dirname, '../static');
const out = process.env.ALBUM_QA_OUTPUT || '/tmp/zhuapic-album-qa';
const preview = process.argv.includes('--preview');
fs.mkdirSync(out, { recursive: true });
const images = Array.from({ length: 19 }, (_, i) => ({
  id: i + 1, public_name: `${i + 1}.png`, original_name: i === 0 ? '夏日旅途_<test>&".png' : `旅行手记_${String(i + 1).padStart(2, '0')}.png`,
  url: `https://old.example/i/${i + 1}.png`, thumb_medium: `/fixture/${i % 4}.png`,
  date: i < 8 ? '2026-09-07' : i < 14 ? '2026-09-06' : '2026-08-29',
  created_at: '2026-09-07T10:00:00+08:00', width: 1200, height: 800,
  mime_type: 'image/png', storage_mode: ['local', 's3', 'mirror'][i % 3], size: 1800000 + i * 100,
  is_animated: i === 2, username: 'Preview',
}));
(async () => {
  const fixtures = await Promise.all(['#72abb3', '#c9a0b4', '#7f9970', '#c2a15b'].map((color, i) => sharp(Buffer.from(`<svg width="800" height="600"><rect width="800" height="600" fill="${color}"/><path d="M0 600 L0 420 L220 180 L430 420 L560 270 L800 470 L800 600Z" fill="#263b42"/><circle cx="${560-i*60}" cy="140" r="65" fill="#f0d8a5"/></svg>`)).png().toBuffer()));
  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://localhost');
    if (preview && url.pathname.startsWith('/api/')) {
      res.setHeader('Content-Type', 'application/json');
      if (req.method !== 'GET') { res.statusCode = 403; return res.end(JSON.stringify({ error: '只读演示，不保存修改' })); }
      let data = {};
      if (url.pathname.endsWith('/images')) {
        const q = url.searchParams.get('q'), storage = url.searchParams.get('storage');
        data = { images: images.filter(im => (!q || im.original_name.includes(q)) && (!storage || im.storage_mode === storage)) };
      } else if (url.pathname === '/api/me') data = { user: { id: 1, username: 'Preview', email: '只读演示 · 与生产环境隔离', role: 'admin' }, upload_policy: {}, upload_usage: {} };
      else if (url.pathname === '/api/user/dashboard') data = { metrics: { images: 19, image_bytes: 362000000, uploads_today: 5, bytes_today: 25000000, local_images: 7, s3_images: 10, mirror_images: 2 }, activity: Array.from({ length: 14 }, (_, i) => { const date = new Date(); date.setDate(date.getDate() - 13 + i); return { date: date.toISOString().slice(5, 10), count: [0,2,0,3,1,0,0,2,1,4,0,1,0,5][i] }; }) };
      else if (url.pathname.endsWith('/settings')) data = { settings: { site_name: 'Origin', s3_configured: 'true', admin_path: '/admin.html' } };
      else if (url.pathname.endsWith('/public')) data = { site_name: 'Origin' };
      return res.end(JSON.stringify(data));
    }
    if (preview && url.pathname === '/') { res.writeHead(302, { Location: '/index.html' }); return res.end(); }
    if (preview && ['/admin', '/dashboard'].includes(url.pathname)) url.pathname += '.html';
    if (url.pathname === '/assets/test-background.png' || url.pathname.startsWith('/fixture/') || url.pathname.startsWith('/i/')) {
      res.setHeader('Content-Type', 'image/png'); return res.end(fixtures[parseInt(url.pathname.match(/\d+/)?.[0] || '0') % 4]);
    }
    const file = path.resolve(root, '.' + url.pathname);
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) { res.statusCode = 404; return res.end(); }
    res.setHeader('Content-Type', ({ '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript', '.svg': 'image/svg+xml' })[path.extname(file)] || 'application/octet-stream');
    if (preview && file.endsWith('.html')) return res.end(fs.readFileSync(file, 'utf8').replace('<head>', '<head><script>localStorage.setItem("zhuapic_token","isolated-preview")</script>'));
    fs.createReadStream(file).pipe(res);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  if (preview) { console.log(`Read-only album preview: ${origin}/dashboard.html#photos`); return; }
  const browser = await chromium.launch({ channel: 'chrome', headless: true });
  try {
    const brandingContext = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const branded = { site_name: 'Origin', site_tagline: 'Origin', hero_title: 'Origin', hero_subtitle: '', announcement: '', footer_text: 'Powered by Yuki.', default_accent_color: 'blue', max_upload_mb: '50', upload_policy: { max_file_mb: 50, max_batch_count: 20 } };
    let brandingSettings = { ...branded }, failSettings = false, failMe = false, releaseBranding;
    let brandingGate = new Promise(resolve => { releaseBranding = resolve; });
    const brandErrors = [];
    await brandingContext.route('**/api/**', async route => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname.endsWith('/public')) {
        await brandingGate;
        return route.fulfill({ status: failSettings ? 503 : 200, json: failSettings ? { error: 'unavailable' } : brandingSettings });
      }
      if (pathname === '/api/me') return route.fulfill({ status: failMe ? 503 : 200, json: { user: { role: 'user' }, upload_policy: { max_file_mb: 80, max_batch_count: 10 } } });
      return route.fulfill({ json: {} });
    });
    const brandPage = await brandingContext.newPage();
    brandPage.on('pageerror', err => brandErrors.push(err.message));
    await brandPage.goto(`${origin}/index.html`);
    assert.equal(await brandPage.locator('#sitePage').isVisible(), false, 'do not paint template branding before settings');
    assert.equal(await brandPage.locator('#siteLoadState').isVisible(), true);
    assert.doesNotMatch(await brandPage.locator('body').textContent(), /抓住图片|为 img.example.com|Powered by ZhuaPic|ZhuaPic/);
    assert.equal(await brandPage.locator('#heroTitle').textContent(), '');
    await brandPage.screenshot({ path: `${out}/branding-cold-start.png`, animations: 'disabled' });
    releaseBranding();
    await brandPage.waitForFunction(() => document.body.dataset.siteState === 'ready');
    assert.equal(await brandPage.title(), 'Origin');
    assert.equal(await brandPage.locator('#siteName').textContent(), 'Origin');
    assert.equal(await brandPage.locator('#maxUploadLabel').textContent(), '50MB');
    assert.equal(await brandPage.locator('#heroSubtitle').isVisible(), false);
    assert.equal(await brandPage.locator('#announcement').isVisible(), false);
    await brandPage.screenshot({ path: `${out}/branding-ready.png`, animations: 'disabled' });
    await brandPage.evaluate(() => window.OrimgBackground.apply({ background_url: '/assets/test-background.png', background_overlay: 75, background_blur: 3 }));
    assert.equal(await brandPage.locator('.site-wallpaper').isVisible(), true);
    for (const mode of ['light', 'dark']) {
      await brandPage.getByRole('button', { name: '外观设置', exact: true }).click();
      await brandPage.locator(`#appearanceDialog .appearance-choice:has(input[value="${mode}"])`).click();
      await brandPage.locator('#appearanceDialog .appearance-choice:has(input[value="clear"])').click();
      await brandPage.getByRole('button', { name: '完成', exact: true }).click();
      await brandPage.screenshot({ path: `${out}/background-${mode}.png`, animations: 'disabled' });
      const withImage = await sharp(await brandPage.screenshot()).raw().toBuffer({ resolveWithObject: true });
      await brandPage.evaluate(() => delete document.body.dataset.wallpaper);
      const plain = await sharp(await brandPage.screenshot()).raw().toBuffer({ resolveWithObject: true });
      const offset = (100 * withImage.info.width + 10) * withImage.info.channels;
      assert(Math.abs(withImage.data[offset] - plain.data[offset]) > 5, 'wallpaper is actually visible behind content');
      await brandPage.evaluate(() => document.body.dataset.wallpaper = '');
    }
    await brandPage.setViewportSize({ width: 390, height: 844 });
    await brandPage.screenshot({ path: `${out}/background-mobile.png`, animations: 'disabled' });
    assert.equal(await brandPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
    await brandPage.setViewportSize({ width: 1440, height: 1000 });
    assert.equal(await brandPage.evaluate(() => window.OrimgBackground.apply({ background_url: '/assets/missing-background.png' })), false);
    assert.equal(await brandPage.locator('.site-wallpaper').isVisible(), false, 'failed background falls back to plain theme');
    assert.equal(await brandPage.evaluate(() => window.OrimgBackground.apply({ background_url: 'javascript:alert(1)' })), false);
    brandingSettings = { ...branded, hero_title: '', site_tagline: '', footer_text: '' };
    await brandPage.reload();
    await brandPage.waitForFunction(() => document.body.dataset.siteState === 'ready');
    for (const id of ['heroTitle','heroSubtitle','announcement','siteTagline','footerText']) {
      assert.equal(await brandPage.locator(`#${id}`).textContent(), '', id + ' stays explicitly empty');
      assert.equal(await brandPage.locator(`#${id}`).isVisible(), false);
    }
    failSettings = true;
    await brandPage.reload();
    await brandPage.waitForFunction(() => document.body.dataset.siteState === 'error');
    assert.equal(await brandPage.locator('#sitePage').isVisible(), false);
    assert.equal(await brandPage.locator('#retrySiteLoad').isVisible(), true);
    assert.equal(await brandPage.locator('#siteName').textContent(), '', 'no stale branding cache on failure');
    failSettings = false;
    await brandPage.locator('#retrySiteLoad').click();
    await brandPage.waitForFunction(() => document.body.dataset.siteState === 'ready');
    failMe = true;
    await brandPage.evaluate(() => localStorage.setItem('zhuapic_token', 'keep-on-network-error'));
    await brandPage.reload();
    await brandPage.waitForFunction(() => document.body.dataset.siteState === 'error');
    assert.equal(await brandPage.evaluate(() => localStorage.getItem('zhuapic_token')), 'keep-on-network-error');
    failMe = false;
    await brandPage.locator('#retrySiteLoad').click();
    await brandPage.waitForFunction(() => document.body.dataset.siteState === 'ready');
    assert.equal(await brandPage.locator('#maxUploadLabel').textContent(), '80MB', 'first visible policy belongs to current user');
    await brandPage.evaluate(() => localStorage.removeItem('zhuapic_token'));
    for (const loginPage of ['admin', 'dashboard']) {
      await brandPage.goto(`${origin}/${loginPage}.html`);
      await brandPage.waitForFunction(() => !document.body.hasAttribute('data-brand-pending'));
      assert.equal(await brandPage.locator('#loginShell [data-site-name]').textContent(), 'Origin');
      assert.match(await brandPage.title(), /^Origin/);
    }
    assert.deepEqual(brandErrors, []);
    await brandingContext.close();
    const noScriptContext = await browser.newContext({ javaScriptEnabled: false });
    const noScriptPage = await noScriptContext.newPage();
    await noScriptPage.goto(`${origin}/index.html`);
    assert.equal(await noScriptPage.locator('noscript').isVisible(), true);
    assert.doesNotMatch(await noScriptPage.locator('body').textContent(), /抓住图片|为 img.example.com|Powered by ZhuaPic/);
    await noScriptContext.close();
    // Delayed and failed session checks must never show the login form prematurely.
    for (const area of ['dashboard', 'admin']) {
      const authContext = await browser.newContext();
      await authContext.addInitScript(() => { if (!sessionStorage.getItem('auth-test-seeded')) { localStorage.setItem('zhuapic_token', 'session-test'); sessionStorage.setItem('auth-test-seeded', '1'); } });
      let authStatus = 200, releaseSession;
      let authGate = new Promise(resolve => { releaseSession = resolve; });
      await authContext.route('**/api/**', async route => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/me') {
          await authGate;
          return route.fulfill({ status: authStatus, json: { user: { username: 'Session', email: 'session@example.test', role: 'admin' } } });
        }
        return route.fulfill({ json: path.endsWith('/public') ? branded : path.endsWith('/settings') ? { settings: branded } : {} });
      });
      const authPage = await authContext.newPage();
      await authPage.goto(`${origin}/${area}.html`);
      await authPage.waitForTimeout(200);
      assert.equal(await authPage.locator('#loginShell').isVisible(), false, 'login stays hidden during auth');
      assert.equal(await authPage.locator('#authStatus').isVisible(), true);
      releaseSession();
      await authPage.locator(area === 'dashboard' ? '#dashboardShell' : '#adminLayout').waitFor();
      assert.equal(await authPage.locator('#loginShell').isVisible(), false);
      await authPage.getByRole('button', { name: '账号菜单', exact: true }).click();
      assert.equal(await authPage.locator('.account-menu:popover-open').getByRole('menuitem', { name: '账号设置' }).count(), 1);
      assert.equal(await authPage.locator('#logoutButton').count(), 0);
      await authPage.keyboard.press('Escape');
      authStatus = 503;
      await authPage.reload();
      await authPage.locator('#retrySession').waitFor();
      assert.equal(await authPage.evaluate(() => localStorage.getItem('zhuapic_token')), 'session-test');
      assert.equal(await authPage.locator('#loginShell').isVisible(), false);
      authStatus = 200;
      await authPage.locator('#retrySession').click();
      await authPage.locator(area === 'dashboard' ? '#dashboardShell' : '#adminLayout').waitFor();
      authStatus = 401;
      await authPage.reload();
      await authPage.locator('#loginShell').waitFor();
      assert.equal(await authPage.evaluate(() => localStorage.getItem('zhuapic_token')), null);
      authStatus = 200;
      await authPage.evaluate(() => localStorage.setItem('zhuapic_token', 'session-test'));
      await authPage.reload();
      await authPage.getByRole('button', { name: '账号菜单', exact: true }).click();
      await authPage.getByRole('menuitem', { name: '退出登录', exact: true }).click();
      await authPage.locator('#loginShell').waitFor();
      assert.equal(await authPage.evaluate(() => localStorage.getItem('zhuapic_token')), null, 'logout clears the session');
      await authContext.close();
    }
    console.log('Session initialization: delayed auth, network retry and expired sessions passed');
    console.log('Branding: delayed cold start, explicit empty content, failure/retry, policy readiness, login branding and no-JS passed');
    for (const admin of [false, true]) {
      let rows = [...images];
      let siteAccent = 'purple';
      let backgroundSettings = {};
      let profileName = 'Preview';
      const errors = [], requests = [];
      const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, permissions: ['clipboard-read', 'clipboard-write'] });
      await context.addInitScript(() => localStorage.setItem('zhuapic_token', 'isolated-ui-test'));
      await context.route('**/api/**', async route => {
        const req = route.request(), u = new URL(req.url());
        requests.push({ path: u.pathname, method: req.method() });
        let data = {};
        if (u.pathname === '/api/admin/background') data = { url: '/assets/test-background.png' };
        else if (u.pathname === '/api/upload') data = { ...images[0], url: `${origin}/i/1.png` };
        else if (u.pathname.endsWith('/images/bulk')) { const ids = req.postDataJSON().ids; rows = rows.filter(im => !ids.includes(im.id)); data = { deleted: ids.length }; }
        else if (/\/images\/\d+$/.test(u.pathname) && req.method() === 'DELETE') { rows = rows.filter(im => im.id !== Number(u.pathname.split('/').pop())); }
        else if (u.pathname.endsWith('/images')) {
          let filtered = rows.filter(im => !u.searchParams.get('q') || im.original_name.includes(u.searchParams.get('q')));
          if (u.searchParams.get('storage')) filtered = filtered.filter(im => im.storage_mode === u.searchParams.get('storage'));
          const cursor = Number(u.searchParams.get('cursor') || 0);
          data = { images: filtered.slice(cursor, cursor + 14), has_more: filtered.length > cursor + 14, next_cursor: String(cursor + 14) };
        }
        else if (u.pathname === '/api/me') {
          if (req.method() === 'PUT') profileName = req.postDataJSON().username;
          data = { user: { id: 1, username: profileName, email: 'preview@example.test', role: admin ? 'admin' : 'user' }, upload_policy: {}, upload_usage: {} };
        }
        else if (u.pathname === '/api/user/dashboard') data = { metrics: { images: 19, image_bytes: 362000000, uploads_today: 5, bytes_today: 25000000, local_images: 7, s3_images: 10, mirror_images: 2 }, activity: [{ date: new Date().toISOString().slice(5,10), count: 5 }] };
        else if (u.pathname.endsWith('/settings')) {
          if (req.method() === 'PUT') {
            const payload = req.postDataJSON();
            siteAccent = payload.default_accent_color || siteAccent;
            backgroundSettings = Object.fromEntries(['background_url','background_overlay','background_blur'].filter(key => key in payload).map(key => [key,payload[key]]));
          }
          data = { settings: { site_name: 'Origin', s3_configured: 'true', default_accent_color: siteAccent, ...backgroundSettings } };
        }
        else if (u.pathname.endsWith('/public')) data = { site_name: 'Origin', default_accent_color: siteAccent };
        await route.fulfill({ json: data });
      });
      const page = await context.newPage();
      page.on('pageerror', err => errors.push(err.message));
      page.on('dialog', dialog => dialog.accept());
      await page.goto(`${origin}/${admin ? 'admin' : 'dashboard'}.html#${admin ? 'images' : 'photos'}`);
      await page.locator('.ag-tile').first().waitFor();
      assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), 'purple', 'fresh browser uses site default');
      await page.waitForFunction(() => document.querySelector('.brand-logo').classList.contains('appearance-logo'));
      await page.getByRole('button', { name: '外观设置', exact: true }).click();
      await page.locator('#appearanceDialog').waitFor();
      for (const mode of ['light', 'dark']) {
        await page.locator(`.appearance-choice:has(input[value="${mode}"])`).click();
        for (const color of ['lime','blue','purple','pink','red','orange','yellow','green','graphite']) {
          await page.locator(`#appearanceDialog .appearance-swatch:has(input[value="${color}"])`).click();
          assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), color);
          assert.equal(await page.locator('.brand-logo').first().evaluate(el => getComputedStyle(el).backgroundColor), await page.evaluate(() => {
            const el = document.createElement('span'); el.style.color = 'var(--accent)'; document.body.append(el); const color = getComputedStyle(el).color; el.remove(); return color;
          }), 'logo follows accent');
          const contrast = await page.locator('#appearanceDialog .primary-button').evaluate(el => {
            const style = getComputedStyle(el);
            const luminance = value => value.match(/[\d.]+/g).slice(0,3).map(Number).map(n => n / 255).map(n => n <= .04045 ? n / 12.92 : ((n + .055) / 1.055) ** 2.4).reduce((sum,n,i) => sum + n * [.2126,.7152,.0722][i], 0);
            const a = luminance(style.color), b = luminance(style.backgroundColor);
            return (Math.max(a,b) + .05) / (Math.min(a,b) + .05);
          });
          assert(contrast >= 4.5, `${mode}/${color} button contrast ${contrast}`);
        }
      }
      await page.locator('#appearanceDialog .appearance-swatch:has(input[value="blue"])').click();
      await page.locator('.appearance-choice:has(input[value="clear"])').click();
      await page.locator('#appearanceHighlight').selectOption('pink');
      await page.locator('#appearanceMotion').uncheck();
      assert.equal(await page.evaluate(() => document.documentElement.dataset.motion), 'reduced');
      await page.screenshot({ path: `${out}/appearance-${admin ? 'admin' : 'user'}-dark.png`, animations: 'disabled' });
      const other = await context.newPage();
      await other.goto(`${origin}/index.html`);
      assert.equal(await other.evaluate(() => document.documentElement.dataset.accent), 'blue');
      await page.locator('#appearanceDialog .appearance-swatch:has(input[value="red"])').click();
      await other.waitForFunction(() => document.documentElement.dataset.accent === 'red');
      await other.close();
      await page.locator('.appearance-choice:has(input[value="auto"])').click();
      await page.emulateMedia({ colorScheme: 'dark' });
      await page.waitForFunction(() => document.documentElement.style.colorScheme === 'dark');
      await page.emulateMedia({ colorScheme: 'light' });
      await page.waitForFunction(() => document.documentElement.style.colorScheme === 'light');
      await page.setViewportSize({ width: 390, height: 844 });
      await page.screenshot({ path: `${out}/appearance-mobile.png`, animations: 'disabled' });
      assert.equal(await page.locator('.appearance-dialog').evaluate(el => el.scrollWidth <= el.clientWidth), true);
      await page.locator('[data-appearance-reset]').click();
      assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), 'purple', 'reset returns to site color');
      await page.evaluate(() => window.ZhuaAppearance.setSiteDefault('orange'));
      assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), 'orange');
      await page.locator('#appearanceDialog .appearance-swatch:has(input[value="blue"])').click();
      await page.evaluate(() => window.ZhuaAppearance.setSiteDefault('green'));
      assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), 'blue', 'personal color wins');
      await page.locator('#appearanceFollowSite').check();
      assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), 'green');
      await page.evaluate(() => { document.querySelector('.brand-logo').src = '/fixture/0.png'; });
      await page.waitForFunction(() => !document.querySelector('.brand-logo').classList.contains('appearance-logo'));
      assert.match(await page.locator('.brand-logo').first().getAttribute('src'), /fixture/);
      await page.evaluate(() => { document.querySelector('.brand-logo').src = '/assets/logo.svg'; });
      await page.getByRole('button', { name: '完成', exact: true }).click();
      await page.setViewportSize({ width: 1440, height: 1000 });
      assert.equal(await page.locator('.ag-tile').count(), 14);
      assert.equal(await page.locator('.ag-filters').isVisible(), false);
      await page.locator('.ag-open img').evaluateAll(async imgs => { await Promise.all(imgs.map(img => { img.loading = 'eager'; return img.decode().catch(() => {}); })); });
      await page.screenshot({ path: `${out}/${admin ? 'admin' : 'user'}-desktop.png`, fullPage: true, animations: 'disabled' });
      if (admin) {
        assert.equal(await page.locator('#panelTitle').textContent(), '图库');
        assert.equal(await page.locator('.ag-owner').count(), 14);
        assert.match(await page.locator('.ag-owner').first().textContent(), /Preview/);
      }
      await page.locator('.ag-tile').first().click({ button: 'right' });
      await page.locator('.ag-context').waitFor();
      await page.screenshot({ path: `${out}/${admin ? 'admin' : 'user'}-context.png`, animations: 'disabled' });
      await page.getByRole('menuitem', { name: '复制 Markdown', exact: true }).click();
      assert.match(await page.evaluate(() => navigator.clipboard.readText()), /!\[夏日旅途/);
      await page.locator('.ag-more').first().click();
      await page.keyboard.press('End');
      assert.equal(await page.evaluate(() => document.activeElement.dataset.context), 'delete');
      await page.keyboard.press('Escape');
      assert.equal(await page.locator('.ag-context').isVisible(), false);
      await page.locator('.ag-tile').first().hover();
      await page.locator('.ag-quick-copy').first().click();
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), `${origin}/i/1.png`);
      await page.evaluate(() => { window.originalCopyCommand = document.execCommand; document.execCommand = () => false; });
      await page.locator('.ag-quick-copy').first().click();
      assert.equal(await page.evaluate(() => navigator.clipboard.readText()), `${origin}/i/1.png`, 'Clipboard API fallback');
      await page.evaluate(() => { document.execCommand = window.originalCopyCommand; });
      await page.locator('[data-ag-open="1"]').click();
      await page.locator('.ag-viewer').waitFor();
      await page.locator('.ag-viewer select').selectOption('html');
      await page.locator('[data-ag="copy"]').click();
      assert.match(await page.evaluate(() => navigator.clipboard.readText()), /alt="夏日旅途_&lt;test&gt;&amp;&quot;\.png"/);
      await page.locator('[data-ag="next"]').click();
      assert.match(await page.locator('.ag-viewer-title span').textContent(), /^2 \/ 14/);
      await page.keyboard.press('ArrowRight');
      assert.match(await page.locator('.ag-viewer-title span').textContent(), /^3 \/ 14/);
      await page.locator('[data-ag="info"]').click();
      assert.equal(await page.locator('.ag-details').isVisible(), true);
      await page.locator('.ag-zoom').fill('200');
      assert.equal(await page.locator('.ag-viewer output').textContent(), '200%');
      await page.locator('.ag-stage').hover();
      await page.mouse.wheel(0, -150);
      await page.waitForFunction(() => Number(document.querySelector('.ag-zoom').value) > 200);
      await page.screenshot({ path: `${out}/${admin ? 'admin' : 'user'}-viewer.png`, animations: 'disabled' });
      await page.keyboard.press('Escape');
      assert.equal(await page.locator('.ag-viewer').isVisible(), false);
      await page.locator('.ag-day-select').first().click();
      assert.equal(await page.locator('.ag-selected').count(), 8);
      await page.locator('.ag-copy-format').selectOption('markdown');
      await page.locator(admin ? '#copyAdminSelected' : '#copySelectedImages').click();
      assert.equal((await page.evaluate(() => navigator.clipboard.readText())).split('\n').length, 8);
      await page.locator(admin ? '#clearAdminSelection' : '#clearImageSelection').click();
      await page.locator('.ag-size input').fill('240');
      await page.locator('[data-ag="fit"]').click();
      assert.equal(await page.locator('.ag-gallery').getAttribute('data-fit'), 'contain');
      await page.locator('[data-ag="filters"]').click();
      await page.locator(admin ? '#adminImageSearch' : '#imageSearch').fill('no-such-image');
      await page.locator('.ag-filters button[type="submit"]').click();
      await page.locator('.ag-empty').waitFor();
      await page.locator('[data-ag="reset-filters"]').click();
      await page.locator('.ag-tile').first().waitFor();
      await page.locator('[data-ag="filters"]').click();
      await page.locator(admin ? '#loadMoreAdminImages' : '#loadMoreImages').click();
      await page.waitForFunction(() => document.querySelectorAll('.ag-tile').length === 19);
      // Permission-scoped mutation is exercised only against the in-memory API fixture.
      await page.locator('.ag-day-select').first().click();
      await page.locator(admin ? '#deleteAdminSelected' : '#deleteSelectedImages').click();
      await page.waitForFunction(() => document.querySelectorAll('.ag-tile').length === 11);
      assert(requests.some(r => r.path === `/api/${admin ? 'admin' : 'user'}/images/bulk`));
      await page.setViewportSize({ width: 390, height: 844 });
      await page.evaluate(() => document.documentElement.dataset.theme = 'dark');
      await page.locator('.ag-open img').evaluateAll(async imgs => { await Promise.all(imgs.map(img => { img.loading = 'eager'; return img.decode().catch(() => {}); })); });
      await page.screenshot({ path: `${out}/${admin ? 'admin' : 'user'}-mobile-dark.png`, fullPage: true, animations: 'disabled' });
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'mobile overflow');
      await page.locator('.ag-tile').first().hover();
      await page.locator('.ag-more').first().click();
      const menuBounds = await page.locator('.ag-context').boundingBox();
      assert(menuBounds.x >= 0 && menuBounds.x + menuBounds.width <= 390 && menuBounds.y + menuBounds.height <= 844, 'menu stays within viewport');
      await page.getByRole('menuitem', { name: '选择图片', exact: true }).click();
      assert.equal(await page.locator('.ag-selected').count(), 1);
      await page.locator('.ag-open').first().click();
      if (await page.locator('.ag-details').isVisible()) await page.locator('[data-ag="info"]').click();
      const touch = await context.newCDPSession(page);
      await touch.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 280, y: 300 }] });
      await touch.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 100, y: 302 }] });
      await touch.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
      assert.match(await page.locator('.ag-viewer-title span').textContent(), /^2 \/ 11/);
      await touch.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: 150, y: 300 }, { x: 210, y: 300 }] });
      await touch.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: 80, y: 300 }, { x: 280, y: 300 }] });
      await touch.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
      assert(Number(await page.locator('.ag-zoom').inputValue()) > 100, 'pinch zoom');
      await touch.detach();
      if (!(await page.locator('.ag-details').isVisible())) await page.locator('[data-ag="info"]').click();
      await page.screenshot({ path: `${out}/${admin ? 'admin' : 'user'}-mobile-viewer.png`, animations: 'disabled' });
      assert.deepEqual(errors, []);
      assert(requests.filter(r => r.method !== 'GET').every(r => r.path.endsWith('/images/bulk')));
      await page.keyboard.press('Escape');
      if (admin) {
        await page.setViewportSize({ width: 1440, height: 1000 });
        for (const panel of ['overview', 'storage', 'users', 'groups', 'site', 'security', 'tasks']) {
          await page.locator(`.admin-nav [data-panel="${panel}"]`).click();
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, panel + ' overflow');
          if (['overview','storage','users'].includes(panel)) await page.screenshot({ path: `${out}/admin-${panel}.png`, animations: 'disabled' });
          if (panel === 'site') {
            await page.locator('.site-accent-swatches .appearance-swatch:has(input[value="pink"])').click();
            assert.equal(await page.locator('[name="default_accent_color"]').inputValue(), 'pink');
            await page.getByRole('button', { name: '保存页面设置', exact: true }).click();
            await page.waitForFunction(() => document.documentElement.dataset.accent === 'pink');
            assert.equal(siteAccent, 'pink', 'site default saved through isolated admin API');
            await page.screenshot({ path: `${out}/admin-site-accent.png`, animations: 'disabled' });
            assert.equal(await page.locator('#useOrimgBrand').count(), 0);
            assert.equal(await page.locator('[name="site_name"]').inputValue(), 'Origin');
            await page.locator('#backgroundFile').setInputFiles({ name: 'background.png', mimeType: 'image/png', buffer: fixtures[0] });
            await page.waitForFunction(() => document.body.hasAttribute('data-wallpaper'));
            assert.equal(await page.locator('[name="background_url"]').inputValue(), '/assets/test-background.png');
            await page.getByRole('button', { name: '保存页面设置', exact: true }).click();
            await page.waitForFunction(() => document.getElementById('backgroundMessage').textContent.includes('预览已更新'));
            assert.equal(backgroundSettings.background_url, '/assets/test-background.png');
            await page.locator('#backgroundPreview').scrollIntoViewIfNeeded();
            await page.screenshot({ path: `${out}/admin-background-editor.png`, animations: 'disabled' });
            await page.locator('#clearBackground').click();
            assert.equal(await page.locator('[name="background_url"]').inputValue(), '');
          }
        }
      } else {
        for (const width of [1440, 390]) {
          await page.setViewportSize({ width, height: 900 });
          await page.evaluate(light => document.documentElement.dataset.theme = light ? "light" : "dark", width === 1440);
          await page.locator('.dashboard-nav [data-page="overview"]').click();
          assert.equal(await page.locator('.dashboard-nav button').count(), 2);
          assert.equal(await page.locator('#fileInput').count(), 0);
          await page.locator(".ag-toast").evaluateAll(items => items.forEach(el => el.hidden = true));
          await page.locator('.recent-image img').evaluateAll(imgs => Promise.all(imgs.map(img => img.decode().catch(() => {}))));
          await page.screenshot({ path: `${out}/dashboard-${width}.png`, fullPage: true, animations: 'disabled' });
          assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'dashboard fits viewport');
          await page.getByRole('button', { name: '账号菜单', exact: true }).click();
          const accountMenu = page.locator('.account-menu:popover-open');
          await accountMenu.waitFor();
          assert.equal(await accountMenu.getByRole('menuitem', { name: '管理后台' }).count(), 0);
          const bounds = await accountMenu.boundingBox();
          assert(bounds.x >= 0 && bounds.x + bounds.width <= width);
          await page.screenshot({ path: `${out}/account-menu-${width}.png`, animations: 'disabled' });
          await accountMenu.getByRole('menuitem', { name: '账号设置' }).click();
          await page.locator('#accountDialog[open]').waitFor();
          assert.equal(await page.locator('#profileForm [name="email"]').inputValue(), 'preview@example.test');
          await page.screenshot({ path: `${out}/account-settings-${width}.png`, animations: 'disabled' });
          assert.equal(await page.locator('#accountDialog').evaluate(el => el.scrollWidth <= el.clientWidth), true);
          await page.keyboard.press('Escape');
          assert.equal(await page.locator('#accountDialog').isVisible(), false);
        }
        await page.getByRole('button', { name: '账号菜单', exact: true }).click();
        await page.getByRole('menuitem', { name: '账号设置', exact: true }).click();
        await page.locator('#profileForm [name="username"]').fill('Tester');
        await page.locator('#profileForm [name="current_password"]').fill('isolated-test-only');
        await page.locator('#profileForm [type="submit"]').click();
        await page.waitForFunction(() => document.getElementById('profileMessage').textContent === '账号已保存');
        assert.equal(await page.locator('.account-avatar').textContent(), 'T');
        assert.equal(await page.locator('#profileForm [name="current_password"]').inputValue(), '');
        await page.keyboard.press('Escape');
        await page.locator('.recent-image').first().click();
        await page.locator('.ag-viewer[open]').waitFor();
        await page.keyboard.press('Escape');
        await page.getByRole('button', { name: '账号菜单', exact: true }).focus();
        await page.keyboard.press('ArrowDown');
        assert.equal(await page.evaluate(() => document.activeElement.textContent), '账号设置');
        await page.keyboard.press('Escape');
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.goto(`${origin}/index.html`);
        await page.locator('.upload-emblem img').waitFor();
        await page.screenshot({ path: `${out}/upload-desktop.png`, animations: 'disabled' });
        await page.locator('#fileInput').setInputFiles({ name: 'preview.png', mimeType: 'image/png', buffer: fixtures[0] });
        assert.equal(await page.locator('#uploadButton').isVisible(), false, 'single image upload control');
        await page.locator('[data-upload-one]').click();
        await page.locator('.queue-item.done').waitFor();
        await page.setViewportSize({ width: 390, height: 844 });
        await page.evaluate(() => document.documentElement.dataset.theme = 'dark');
        await page.screenshot({ path: `${out}/upload-mobile-dark.png`, fullPage: true, animations: 'disabled' });
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'upload mobile overflow');
        for (const uploadPage of ['index.html']) {
          await page.goto(`${origin}/${uploadPage}`);
          await page.locator('#fileInput').setInputFiles([0, 1].map(i => ({ name: `batch-${i}.png`, mimeType: 'image/png', buffer: fixtures[i] })));
          await page.locator('#uploadButton').click();
          await page.waitForFunction(() => document.querySelectorAll('.queue-item.done').length === 2);
          for (const width of [1440, 390]) {
            await page.setViewportSize({ width, height: 844 });
            const summary = page.locator('.copy-menu summary').first();
            await summary.scrollIntoViewIfNeeded();
            await summary.click();
            const menu = page.locator('.copy-menu > div:popover-open');
            await menu.waitFor();
            assert.equal(await menu.evaluate(el => {
              const r = el.getBoundingClientRect();
              return r.left >= 0 && r.right <= innerWidth && r.top >= 0 && r.bottom <= innerHeight && el.contains(document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2));
            }), true, 'copy menu is in viewport and above other rows');
            await page.screenshot({ path: `${out}/copy-${uploadPage.split('.')[0]}-${width}.png`, animations: 'disabled' });
            await menu.getByRole('button', { name: 'Markdown', exact: true }).click();
            assert.match(await page.evaluate(() => navigator.clipboard.readText()), /!\[/);
            await page.waitForFunction(() => !document.querySelector('.copy-menu[open]'));
            await summary.click();
            await menu.waitFor();
            await page.keyboard.press('ArrowDown');
            assert.equal(await page.evaluate(() => document.activeElement.textContent), 'Markdown');
            await page.keyboard.press('Escape');
            await page.waitForFunction(() => !document.querySelector('.copy-menu[open]'));
          }
        }
      }
      assert.deepEqual(errors, []);
      console.log(`${admin ? 'Admin' : 'User'} gallery: copy, viewer, filters, selection, pagination, isolated delete, mobile passed`);
      await context.close();
    }
  } finally { await browser.close(); await new Promise(resolve => server.close(resolve)); }
})().catch(err => { console.error(err); process.exitCode = 1; });
