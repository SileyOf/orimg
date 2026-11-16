// Isolated API fixtures: no real database, object storage, or credentials.
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");
const assert = require("node:assert/strict");
const { chromium } = require("playwright");
const root = path.resolve(__dirname, "../static");
const out = "/tmp/orimg-storage-qa";
fs.mkdirSync(out, { recursive: true });
const settings = { site_name: "Origin", hero_title: "Origin", hero_subtitle: "", footer_text: "", s3_configured: "true", storage_mode: "s3", admin_path: "/admin.html", user_path: "/dashboard.html" };
const user = { id: 1, username: "Preview", email: "preview@example.test", role: "admin", active: true };
let targets = [
  { id: 1, name: "本地主存储", provider: "local", legacy_mode: "local", enabled: true, auto_backup: false, config: { directory: "/www/wwwroot/orimg/data/uploads" }, ready: 19, bytes: 382000000, pending: 0, failed: 0 },
  { id: 2, name: "现有 S3", provider: "s3", legacy_mode: "s3", enabled: true, auto_backup: false, has_secret: true, config: { endpoint: "https://s3.example.test", bucket: "orimg", region: "auto", prefix: "images", access_key: "test-access", path_style: true }, ready: 19, bytes: 382000000, pending: 0, failed: 0 },
  { id: 3, name: "异地 NAS", provider: "local", legacy_mode: "", enabled: true, auto_backup: true, config: { directory: "/mnt/nas/orimg-replicas" }, ready: 18, bytes: 359000000, pending: 1, failed: 0 },
  { id: 4, name: "Cloudflare R2", provider: "r2", legacy_mode: "", enabled: true, auto_backup: true, has_secret: true, config: { endpoint: "https://account.r2.cloudflarestorage.com", bucket: "orimg-backup", region: "auto", prefix: "images", path_style: true }, ready: 17, bytes: 336000000, pending: 1, failed: 1 }
];
let defaultID = 1, writes = [];
let cache = {directory:'/srv/orimg/cache',files:12,bytes:20*1048576,expired_files:3,expired_bytes:4*1048576,ttl_seconds:2592000,scanned_at:new Date().toISOString(),status:'ready',partial:false,largest:[{name:'000000000000000000000001.jpg',bytes:4*1048576,expires_at:new Date().toISOString()}]};
const groups = Array.from({length:30},(_,i) => ({id:i+1,name:'用户组 '+String(i+1).padStart(2,'0'),slug:'group-'+i,is_system:false,member_count:i*12,policy:{id:i+1,active:i%4!==0,allow_storage_selection:i%2===1,max_file_mb:50,max_batch_count:20,storage_mode:'target:1',allowed_types:'jpg,png',total_quota_bytes:1073741824}}));
targets = targets.map(t => ({ ...t, configured: true, location_locked: true, upload_access: t.id === 3 ? "members" : "admin" }));
const tasks = [{ image_id: 10, target_id: 3, filename: "Travel-2026.jpg", target: "异地 NAS", status: "pending", attempts: 0, error: "" }, { image_id: 12, target_id: 4, filename: "Evening-Light.jpg", target: "Cloudflare R2", status: "failed", attempts: 2, error: "目标连接超时，等待重试" }];
function mock(req) {
  const u = new URL(req.url, "http://localhost");
  if (req.method !== "GET") return { ok: true };
  if (u.pathname.endsWith("/public")) return settings;
  if (u.pathname === "/api/me") return process.argv.includes('--preview') ? {user:{...user,group_id:2,group_name:groups[1].name,quota_bytes:12.5*1073741824},upload_policy:{...groups[1].policy,total_quota_bytes:12.5*1073741824},upload_usage:{total_bytes:5*1048576}} : { user };
  if (u.pathname === "/api/storage/options") return { default_name: targets.find(t => t.id === defaultID)?.name, targets: targets.filter(t => user.role === 'admin' || t.id === defaultID || t.upload_access === 'members').map(t => ({ id: t.id, name: t.name, provider: t.provider, is_default: t.id === defaultID })) };
  if (u.pathname === "/api/admin/settings") return { settings };
  if (u.pathname === "/api/mail/status") return { enabled: true,registration_enabled:true };
  if (u.pathname === "/api/admin/mail/registration") return {enabled:false,group_id:2};
  if (u.pathname.startsWith('/api/admin/mail/templates/')) return {template:{subject:'{{site_name}} · 账号验证',title:'账号验证',body:'{{message}}',button:'确认操作',footer:'若非本人操作，请忽略此邮件。'},subject:'Origin · 重设密码',html:fs.existsSync('/tmp/orimg-email-preview.html')?fs.readFileSync('/tmp/orimg-email-preview.html','utf8'):'<p>预览样本尚未生成</p>'};
  if (u.pathname === '/api/auth/captcha') return {question:'4 + 5 = ?',token:'preview-captcha'};
  if (u.pathname === "/api/admin/mail/config") return {config:{enabled:false,host:'smtp.example.test',port:465,tls:'tls',username:'',password:'',from:'no-reply@example.test',name:'Origin',base_url:'https://img.example.com'},tested:false,has_password:false,worker_enabled:true};
  if (u.pathname === "/api/admin/mail/jobs") return {jobs:[{id:1,kind:'test',recipient:'p***@example.test',status:'accepted',attempts:1,created_at:new Date().toISOString(),error:''}],next_before:0};
  if (u.pathname === "/api/admin/storage/targets") return { targets, default_target_id: defaultID, tasks };
  if (u.pathname === "/api/admin/groups") return { groups, guest_group_id: 3 };
  if (u.pathname === "/api/admin/storage/cache") return {cache,cleanup_enabled:true,cleanup_interval_seconds:600};
  if (u.pathname === "/api/admin/users") return { users: [user] };
  return {};
}
(async () => {
  const server = http.createServer((req, res) => {
    if (req.url.startsWith("/api/")) {
      res.setHeader("Content-Type", "application/json");
      if (req.method !== "GET") { res.writeHead(403); res.end(JSON.stringify({ error: "只读演示，不保存修改" })); return; }
      res.end(JSON.stringify(mock(req))); return;
    }
    let pathname=new URL(req.url,"http://localhost").pathname;
    if(['/forgot-password','/register','/account-action','/dashboard'].includes(pathname))pathname+='.html';
    const file = path.resolve(root, "." + pathname);
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) { res.writeHead(404); res.end(); return; }
    res.setHeader("Content-Type", ({ ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml" })[path.extname(file)] || "application/octet-stream");
    if (process.argv.includes("--preview") && file.endsWith(".html")) {
      res.end(fs.readFileSync(file, "utf8").replace("<head>", '<head><script>localStorage.setItem("zhuapic_token","isolated-storage-preview")</script>')); return;
    }
    fs.createReadStream(file).pipe(res);
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  const origin = "http://127.0.0.1:" + server.address().port;
  if (process.argv.includes("--preview")) { console.log(origin + "/admin.html#storage"); return; }
  const browser = await chromium.launch({ channel: "chrome", headless: true });
  try {
    const context = await browser.newContext();
    await context.addInitScript(() => localStorage.setItem("zhuapic_token", "fixture"));
    let gate;
    await context.route("**/api/**", async route => {
      const req = route.request(), pathname = new URL(req.url()).pathname;
      if (req.method() === "GET") return route.fulfill({ json: mock({ url: req.url(), method: "GET" }) });
      if (pathname === "/api/upload") {
        assert(req.postData().includes('target:3'), 'chosen storage not submitted');
        writes.push({ pathname });
        return route.fulfill({ json: { url: origin + '/i/fixture.svg', image: { id: 99 } } });
      }
      const body = req.postData() ? req.postDataJSON() : {};
      writes.push({ pathname, method: req.method(), body });
      if (gate) await gate;
      const match = pathname.match(/targets\/(\d+)/);
      const id = match ? Number(match[1]) : 0;
      if (pathname.endsWith("/targets") && req.method() === "POST") targets.push({ ...body, id: 5, legacy_mode: "", ready: 0, bytes: 0, pending: 0, failed: 0 });
      if (req.method() === "PUT") targets = targets.map(t => t.id === id ? { ...t, ...body } : t);
      if (req.method() === "DELETE") targets = targets.filter(t => t.id !== id);
      if (body.action === "default") defaultID = id;
      return route.fulfill({ json: { ok: true, queued: 2 } });
    });
    const page = await context.newPage(); const errors = [];
    page.on("pageerror", err => errors.push(err.message));
    await page.goto(origin + "/admin.html#storage");
    await page.locator(".storage-target").first().waitFor();
    assert.equal(await page.locator("#adminStatus").count(), 0);
    assert.equal(await page.locator('#storageForm [name="s3_endpoint"], #storageForm [name="local_upload_dir"], #storageForm [name="storage_mode"]').count(), 0, "duplicate storage configuration remains");
    for (const [width, height] of [[390,844], [768,1024], [1440,1000], [2560,1440]]) {
      await page.setViewportSize({ width, height });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "horizontal overflow " + width);
      await page.screenshot({ path: out + "/storage-" + width + ".png", fullPage: true });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.locator("#addStorageTarget").click();
    await page.locator('#targetEditor [name="provider"]').selectOption("r2");
    await page.locator('#targetEditor [name="name"]').fill("R2 测试");
    await page.locator('#targetEditor [name="endpoint"]').fill("https://account.r2.cloudflarestorage.com");
    await page.locator('#targetEditor [name="bucket"]').fill("isolated");
    await page.locator('#targetEditor [name="access_key"]').fill("fixture");
    await page.locator('#targetEditor [name="secret_key"]').fill("fixture");
    await page.screenshot({ path: out + "/target-editor.png" });
    page.once("dialog", d => d.dismiss());
    await page.locator('#targetEditor button[type="submit"]').click();
    assert.equal(writes.length, 0, "cancel performed write");
    let release; gate = new Promise(resolve => release = resolve);
    page.once("dialog", d => d.accept());
    await page.locator('#targetEditor button[type="submit"]').click();
    await page.waitForFunction(() => document.querySelector('#targetEditor button[type="submit"]').disabled);
    await page.evaluate(() => document.querySelector("#targetEditorForm").dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })));
    await page.waitForTimeout(50);
    assert.equal(writes.length, 1, "duplicate save");
    release(); gate = null;
    await page.waitForFunction(() => !document.querySelector("#targetEditor").open);
    assert.equal(await page.locator(".storage-target").count(), 5);
    page.once("dialog", d => d.accept());
    await page.locator('[data-default="5"]').click();
    await page.waitForFunction(() => document.querySelector("#primaryTargetName").textContent === "R2 测试");
    page.once("dialog", d => d.accept());
    await page.locator('[data-auto="5"]').click();
    await page.waitForFunction(() => document.querySelector("#backupTargetNames").textContent.includes("R2 测试"));
    await page.locator('[data-edit="5"]').click();
    assert(await page.locator('#targetEditor [name="endpoint"]').isDisabled(), "location mutable");
    await page.locator('#targetEditor [data-close]').first().click();
    await page.locator('[data-edit="2"]').click();
    assert.equal(await page.locator('#targetEditor [name="secret_key"]').isDisabled(), false, "existing S3 credentials not editable here");
    assert(await page.locator('#targetEditor [name="bucket"]').isDisabled(), "referenced bucket mutable");
    await page.locator('#targetEditor [data-close]').first().click();
    page.once("dialog", d => d.accept());
    await page.locator('[data-access="2"]').click();
    await page.waitForFunction(() => document.querySelector('[data-access="2"]').checked);
    assert.equal(targets.find(t => t.id === 2).upload_access, "members");
    await page.locator('.admin-nav [data-panel="tasks"]').click();
    await page.locator("#targetTasks").waitFor();
    await page.screenshot({ path: out + "/tasks.png", fullPage: true });
    await page.locator('.admin-nav [data-panel="storage"]').click();
    await page.waitForFunction(() => document.querySelector('#cacheBytes').textContent === '20.0 MB');
    assert.equal(await page.locator('#cacheExpired').textContent(),'4.0 MB');
    assert.equal(await page.locator('#cacheDirectory').textContent(),'/srv/orimg/cache');
    await page.locator('#cacheDetails summary').click();
    assert.equal(await page.locator('#cacheFilesBody tr').count(),1);
    cache={...cache,partial:true}; await page.locator('#refreshCache').click();
    await page.waitForFunction(() => document.querySelector('#cacheBytes').textContent.startsWith('≥'));
    cache={...cache,partial:false,status:'unavailable'}; await page.locator('#refreshCache').click();
    await page.waitForFunction(() => document.querySelector('#cacheBytes').textContent === '不可用');
    cache={...cache,status:'ready'}; await page.locator('#refreshCache').click();
    await page.waitForFunction(() => document.querySelector('#cacheBytes').textContent === '20.0 MB');
    for (const view of ['cards','list','compact']) {
      await page.locator('#storageViewTools [data-view="'+view+'"]').click();
      for (const width of [390,768,1440,2560]) {
        await page.setViewportSize({width,height:1000});
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),'storage '+view+' overflow '+width);
      }
      await page.setViewportSize({width:1440,height:1000});
      await page.screenshot({path:out+'/storage-'+view+'.png',fullPage:true});
    }
    await page.locator('#storageViewTools input').fill('no-such-target');
    assert(await page.locator('#storageTargetsEmpty').isVisible());
    await page.locator('#storageViewTools input').fill('');
    await page.reload(); await page.locator('.storage-target').first().waitFor();
    assert.equal(await page.locator('#storageTargets').getAttribute('data-view'),'compact','view preference lost');
    await page.locator('.admin-nav [data-panel="groups"]').click();
    assert.equal(await page.locator('.group-entry').count(),30);
    assert.equal(await page.locator('.policy-card').count(),0,'all group forms rendered eagerly');
    for (const view of ['cards','list','compact']) {
      await page.locator('#groupViewTools [data-view="'+view+'"]').click();
      for (const width of [390,768,1440,2560]) {
        await page.setViewportSize({width,height:1000});
        assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),'groups '+view+' overflow '+width);
      }
      await page.setViewportSize({width:1440,height:1000});
      await page.screenshot({path:out+'/groups-'+view+'.png'});
    }
    await page.locator('#groupViewTools input').fill('用户组 02');
    assert.equal(await page.locator('.group-entry:visible').count(),1);
    await page.locator('.group-entry:visible .group-summary').click();
    await page.locator('.policy-card').waitFor();
    await page.locator('.policy-card [name="group_name"]').fill('未保存的名称');
    page.once('dialog',d=>d.dismiss());await page.locator('.group-dialog [aria-label="关闭用户组编辑"]').click();
    assert.equal(await page.locator('.policy-card [name="group_name"]').inputValue(),'未保存的名称','cancelled close discarded edits');
    for (const width of [390,768,1440]) {
      await page.setViewportSize({width,height:1000});
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),'group editor overflow '+width);
    }
    page.once('dialog',d=>d.accept());await page.locator('.group-dialog [aria-label="关闭用户组编辑"]').click();
    await page.locator('#groupViewTools [data-view="list"]').click();
    await page.locator('#groupViewTools input').fill('');
    await page.locator('#groupViewTools select').selectOption('paused');
    assert.equal(await page.locator('.group-entry:visible').count(),8);
    assert.equal(await page.locator('#groupViewTools option[value="system"]').count(),0);
    user.role = 'user';
    targets.find(t => t.id === 3).name = '异地存储-' + 'LongStorageName'.repeat(5);
    await page.goto(origin + "/index.html");
    await page.waitForFunction(() => document.body.dataset.siteState === "ready");
    assert.equal(await page.locator('#storageSelect option').count(), 4, 'member storage choices incorrect');
    await page.setViewportSize({width:390,height:844});
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'long storage name overflows mobile upload');
    await page.screenshot({ path: out + '/upload-mobile.png', fullPage:true });
    await page.setViewportSize({width:1440,height:1000});
    await page.locator('#storageSelect').selectOption('target:3');
    await page.locator('#fileInput').setInputFiles({ name: 'selection.svg', mimeType: 'image/svg+xml', buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>') });
    await page.screenshot({ path: out + "/upload-selection.png", fullPage: true });
    await page.locator('[data-upload-one]').click();
    await page.waitForFunction(() => document.querySelector('#uploadMessage').textContent === '全部上传完成');
    assert(writes.some(w => w.pathname === '/api/upload'));
    user.role='admin';
    targets.push(...Array.from({length:40},(_,i) => ({...targets[0],id:100+i,name:'数据中心 '+i})));
    await page.evaluate(() => localStorage.removeItem('orimg.storageView'));
    await page.goto(origin+'/admin.html#storage');
    await page.locator('.storage-target').first().waitFor();
    assert.equal(await page.locator('#storageTargets').getAttribute('data-view'),'list','large inventory must default to list');
    assert.equal(errors.length, 0, errors.join("\n"));
    console.log("PASS cache states, 3 views at 4 widths, 30 groups, 45 targets, search/filter, lazy editing, persisted preferences, upload and save controls");
  } finally { await browser.close(); server.close(); }
})().catch(err => { console.error(err); process.exitCode = 1; });
