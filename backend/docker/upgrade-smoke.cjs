// Upgrade test: a v0.6.0 site with local, WebDAV and an unreachable S3
// storage (auto backup) is upgraded in place to the current image.
//
//   GO_BIN=$(which go) node docker/upgrade-smoke.cjs
//
// Uses only disposable resources with unique names and removes them after.
const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'origin-upgrade-qa-'));
  const id = `origin-upgrade-qa-${process.pid}`;
  const oldImage = process.env.ORIGIN_OLD_IMAGE || 'yuki955/orimg:0.6.0';
  const newImage = process.env.ORIGIN_QA_IMAGE || 'yuki955/orimg:0.7.0';
  const go = process.env.GO_BIN || 'go';
  const password = 'isolated-upgrade-fixture-password';
  const resources = { containers: [], volumes: [], network: false };
  const docker = (...args) => execFileSync('docker', args, { encoding: 'utf8', maxBuffer: 16 << 20, timeout: 180000 });
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const sql = query => docker('exec', '-e', `MYSQL_PWD=${password}`, `${id}-db`, 'mysql', '-uroot', '-Nse', query, 'origin').trim();
  async function wait(check, tries = 90) {
    let error;
    for (let i = 0; i < tries; i++) {
      try { return check(); } catch (e) { error = e; await delay(1000); }
    }
    throw error;
  }
  const arch = process.arch === 'arm64' ? 'arm64' : 'amd64';
  const probe = path.join(temp, 'probe');
  const volume = `${id}-data`;
  const app = `${id}-app`;
  const envFile = path.join(temp, 'legacy.env');
  try {
    execFileSync(go, ['build', '-trimpath', '-o', probe, './docker/probe'], {
      cwd: root, env: { ...process.env, GOTOOLCHAIN: 'local', GOPROXY: 'off', CGO_ENABLED: '0', GOOS: 'linux', GOARCH: arch }, stdio: 'inherit'
    });
    docker('network', 'create', '--internal', id); resources.network = true;
    docker('run', '-d', '--name', `${id}-db`, '--network', id, '--network-alias', 'db', '--tmpfs', '/var/lib/mysql',
      '-e', `MYSQL_ROOT_PASSWORD=${password}`, '-e', 'MYSQL_DATABASE=origin', 'mysql:8.4');
    resources.containers.push(`${id}-db`);
    docker('run', '-d', '--name', `${id}-dav`, '--network', id, '--network-alias', 'dav', 'rclone/rclone:1.68.2',
      'serve', 'webdav', '/data', '--addr', ':8080', '--user', 'dav', '--pass', 'dav-pass');
    resources.containers.push(`${id}-dav`);
    await wait(() => sql('SELECT 1'));
    await fs.writeFile(envFile, ['DB_HOST=db', 'DB_USER=root', `DB_PASSWORD=${password}`, 'DB_NAME=origin', 'DB_TLS=disabled',
      `JWT_SECRET=${'upgrade-fixture-signing-key-'.repeat(2)}`, 'BASE_URL=http://localhost:8080', 'ADMIN_USERNAME=owner',
      'ADMIN_EMAIL=owner@example.test', `ADMIN_PASSWORD=${password}`, ''].join('\n'));
    await fs.chmod(envFile, 0o644);
    docker('volume', 'create', volume); resources.volumes.push(volume);
    const start = image => {
      docker('run', '-d', '--name', app, '--network', id, '--read-only', '--cap-drop', 'ALL', '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m',
        '--mount', `type=volume,source=${volume},target=/app/data`, '--mount', `type=bind,source=${envFile},target=/app/.env,readonly`,
        '--mount', `type=bind,source=${probe},target=/qa/probe,readonly`, '-e', 'PORT=8080', image);
      resources.containers.push(app);
    };
    const stop = () => { docker('stop', '-t', '30', app); docker('rm', app); resources.containers = resources.containers.filter(c => c !== app); };
    const api = (method, url, body, token, expected = 200, upload = false) => {
      const r = JSON.parse(execFileSync('docker', ['exec', '-i', app, '/qa/probe'], {
        input: JSON.stringify({ Method: method, Path: url, Body: body, Token: token, Upload: upload }), encoding: 'utf8', timeout: 45000 }));
      assert.equal(r.status, expected, `${method} ${url}: ${r.body}`);
      try { return JSON.parse(r.body); } catch { return r.body; }
    };
    const login = () => {
      const captcha = api('GET', '/api/auth/captcha');
      const [a, b] = captcha.question.match(/\d+/g).map(Number);
      return api('POST', '/api/auth/login', { email: 'owner@example.test', password, captcha_token: captcha.token, captcha_answer: String(a + b) }).token;
    };

    // --- v0.6.0 site -----------------------------------------------------
    start(oldImage);
    await wait(() => api('GET', '/api/ready'));
    let token = login();
    const dav = api('POST', '/api/admin/storage/targets', { name: 'NAS', provider: 'webdav', enabled: true, upload_access: 'members',
      config: { endpoint: 'http://dav:8080/', prefix: 'origin', access_key: 'dav', secret_key: 'dav-pass' } }, token, 201).id;
    const dead = api('POST', '/api/admin/storage/targets', { name: 'Offline S3', provider: 's3', enabled: true, auto_backup: true, upload_access: 'admin',
      config: { endpoint: 'http://unreachable.invalid:9000', region: 'auto', bucket: 'gone', prefix: 'images', access_key: 'a', secret_key: 'b', path_style: true } }, token, 201).id;
    const localID = Number(sql("SELECT id FROM storage_targets WHERE legacy_mode='local'"));
    const onLocal = api('POST', '/api/upload', null, token, 200, true).image.public_name;
    api('POST', `/api/admin/storage/targets/${dav}/actions`, { action: 'default' }, token);
    const onDav = api('POST', '/api/upload', null, token, 200, true).image.public_name;
    api('POST', `/api/admin/storage/targets/${localID}/actions`, { action: 'default' }, token);
    const member = api('GET', '/api/admin/groups', null, token).groups.find(g => g.slug === 'member');
    api('PUT', `/api/admin/policies/${member.policy.id}`, { ...member.policy, storage_mode: `target:${dav}`, allow_storage_selection: true }, token);
    await delay(8000); // let v0.6 try (and fail) the offline backups
    const before = sql("SELECT COUNT(*) FROM images");
    stop();

    // --- upgrade ---------------------------------------------------------
    start(newImage);
    await wait(() => api('GET', '/api/ready'), 120);
    assert.equal(sql('SELECT MAX(version) FROM schema_migrations'), '10');
    for (const table of ['image_replicas', 'storage_targets', 'storage_target_replicas', 'storage_jobs', 'upload_policies']) {
      assert.equal(sql(`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='origin' AND table_name='${table}'`), '0', `${table} survived`);
    }
    assert.equal(sql('SELECT COUNT(*) FROM images'), before);
    const storages = sql('SELECT id, name, provider, auto_backup FROM storages ORDER BY id').split('\n').map(line => line.split('\t'));
    assert.deepEqual(storages.map(s => s[1]).sort(), ['Local', 'NAS', 'Offline S3'], 'unconfigured legacy S3 placeholder should be dropped');
    assert.equal(sql("SELECT value FROM site_settings WHERE `key`='default_storage_id'"), String(localID));
    const copy = (name, storage) => sql(`SELECT c.state FROM image_copies c JOIN images i ON i.id=c.image_id WHERE i.public_name='${name}' AND c.storage_id=${storage}`);
    assert.equal(copy(onLocal, localID), 'ready');
    assert.equal(copy(onDav, dav), 'ready');
    assert.match(copy(onLocal, dead), /pending|failed/);
    assert.equal(sql(`SELECT COALESCE(default_storage_id,0) FROM user_groups WHERE slug='member'`), String(dav));
    assert.equal(sql(`SELECT COUNT(*) FROM group_storages gs JOIN user_groups g ON g.id=gs.group_id WHERE g.slug='member' AND gs.storage_id=${dav}`), '1');
    token = login();
    api('GET', `/i/${onLocal}`);
    api('GET', `/i/${onDav}`);
    const listed = api('GET', '/api/admin/storages', null, token);
    assert.ok(listed.storages.find(s => s.id === dead).auto_backup, 'auto backup flag lost');
    // The offline node that could not be deleted in v0.6 can be removed now.
    const plan = api('GET', `/api/admin/storages/${dead}/removal`, null, token);
    assert.equal(plan.only_here, 0);
    api('POST', `/api/admin/storages/${dead}/removal`, { mode: 'force' }, token);
    assert.equal(sql(`SELECT COUNT(*) FROM storages WHERE id=${dead}`), '0');
    assert.equal(sql(`SELECT COUNT(*) FROM image_copies WHERE storage_id=${dead}`), '0');
    api('GET', `/i/${onLocal}`);
    const fresh = api('POST', '/api/upload', null, token, 200, true);
    assert.ok(fresh.image.storages.some(s => s.id === localID));
    // The app runs in Asia/Shanghai, MySQL in UTC: stored times must follow the app.
    const drift = Number(sql(`SELECT ABS(TIMESTAMPDIFF(MINUTE, created_at, CONVERT_TZ(UTC_TIMESTAMP(), '+00:00', '+08:00'))) FROM images WHERE public_name='${fresh.image.public_name}'`));
    assert.ok(drift <= 2, `database timestamps drift ${drift} minutes from the application zone`);
    // Restart: the migration must not run again or recreate legacy tables.
    stop();
    start(newImage);
    await wait(() => api('GET', '/api/ready'), 60);
    assert.equal(sql("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='origin' AND table_name IN ('image_replicas','storage_jobs','storage_job_items')"), '0');
    api('GET', `/i/${onDav}`);
    console.log('upgrade: v0.6 local + WebDAV + offline S3 with group policies migrated; images served; offline storage removed; restart idempotent');
  } finally {
    for (const name of resources.containers.reverse()) { try { docker('rm', '-f', name); } catch {} }
    for (const name of resources.volumes) { try { docker('volume', 'rm', name); } catch {} }
    if (resources.network) { try { docker('network', 'rm', id); } catch {} }
    await fs.rm(temp, { recursive: true, force: true });
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
