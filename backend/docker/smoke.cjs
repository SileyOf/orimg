const fs = require('node:fs/promises');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');
const { execFileSync, spawnSync } = require('node:child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'origin-docker-qa-'));
  const id = `origin-051-qa-${process.pid}`;
  const resources = { containers: [], volumes: [], network: false };
  const docker = (...args) => execFileSync('docker', args, { encoding: 'utf8', maxBuffer: 16 << 20, timeout: 180000 });
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const password = 'isolated-docker-fixture-password';
  const image = process.env.ORIGIN_QA_IMAGE || 'origin:0.5.1';
  const go = process.env.GO_BIN || 'go';
  const sql = query => docker('exec', '-e', `MYSQL_PWD=${password}`, `${id}-db`, 'mysql', '-uroot', '-Nse', query);
  async function wait(check) {
    let error;
    for (let i = 0; i < 90; i++) {
      try { return check(); } catch (e) { error = e; await delay(1000); }
    }
    throw error;
  }
  try {
    docker('network', 'create', '--internal', id); resources.network = true;
    docker('run', '-d', '--name', `${id}-db`, '--network', id, '--network-alias', 'db', '--tmpfs', '/var/lib/mysql',
      '-e', `MYSQL_ROOT_PASSWORD=${password}`, '-e', 'MYSQL_DATABASE=origin', '-e', 'MYSQL_USER=origin', '-e', `MYSQL_PASSWORD=${password}`, 'mysql:8.4');
    resources.containers.push(`${id}-db`);
    await wait(() => sql('SELECT 1'));
    for (const arch of ['amd64', 'arm64']) {
      const name = `${id}-${arch}`, volume = `${name}-data`, probe = path.join(temp, `probe-${arch}`);
      execFileSync(go, ['build', '-trimpath', '-o', probe, './docker/probe'], {
        cwd: root, env: { ...process.env, GOTOOLCHAIN: 'local', GOPROXY: 'off', CGO_ENABLED: '0', GOOS: 'linux', GOARCH: arch }, stdio: 'inherit'
      });
      docker('volume', 'create', volume); resources.volumes.push(volume);
      const launch = () => {
        docker('run', '-d', '--name', name, '--platform', `linux/${arch}`, '--network', id, '--read-only',
          '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--tmpfs', '/tmp:rw,noexec,nosuid,size=64m',
          '--mount', `type=volume,source=${volume},target=/app/data`, '--mount', `type=bind,source=${probe},target=/qa/probe,readonly`,
          '-e', 'DISABLE_BACKGROUND_WORK=true', image);
        resources.containers.push(name);
      };
      const request = input => JSON.parse(execFileSync('docker', ['exec', '-i', name, '/qa/probe'], {
        input: JSON.stringify(input), encoding: 'utf8', timeout: 45000
      }));
      const api = (method, url, body, token, expected = 200, upload = false) => {
        const r = request({ Method: method, Path: url, Body: body, Token: token, Upload: upload });
        assert.equal(r.status, expected, `${method} ${url}: ${r.body}`);
        try { return JSON.parse(r.body); } catch { return r.body; }
      };
      launch();
      await wait(() => docker('exec', name, '/app/zhuapic', '-healthcheck'));
      assert.equal(docker('exec', name, '/app/zhuapic', '-version').trim(), 'v0.5.1');
      assert.equal(JSON.parse(docker('inspect', name))[0].Config.User, '10001:10001');
      const logResult = spawnSync('docker', ['logs', name], { encoding: 'utf8' });
      assert.equal(logResult.status, 0);
      const logs = logResult.stdout + logResult.stderr;
      // The setup code is intentionally not printed by this test.
      const token = logs.match(/setup code: ([a-f0-9]+)/i)?.[1];
      assert.ok(token, 'setup code not found');
      const dbname = `origin_${arch}`;
      sql(`CREATE DATABASE ${dbname}; GRANT ALL ON ${dbname}.* TO 'origin'@'%'`);
      const form = { host: 'db', port: '3306', user: 'origin', password, name: dbname, tls: 'disabled', base_url: 'http://localhost:8080',
        admin_username: 'owner', admin_email: 'owner@example.test', admin_password: password, confirmed: true };
      assert.equal(api('POST', '/api/install/check', form, token).database.empty, true);
      assert.equal(api('POST', '/api/install/complete', form, token).installed, true);
      api('GET', '/api/ready');
      api('GET', '/install', null, null, 404);
      const captcha = api('GET', '/api/auth/captcha');
      const operands = captcha.question.match(/\d+/g).map(Number);
      const login = api('POST', '/api/auth/login', { email: form.admin_email, password,
        captcha_token: captcha.token, captcha_answer: String(operands[0] + operands[1]) });
      const uploaded = api('POST', '/api/upload', null, login.token, 200, true);
      const imagePath = new URL(uploaded.url).pathname;
      api('GET', imagePath);
      assert.ok(uploaded.image.thumb_small.includes('/t/small/'), 'thumbnail not generated');
      api('GET', new URL(uploaded.image.thumb_small).pathname);
      // Settings are cached briefly; admin writes must still apply immediately.
      api('PUT', '/api/admin/settings', { site_name: `Smoke ${arch}` }, login.token);
      assert.equal(api('GET', '/api/settings/public').site_name, `Smoke ${arch}`);
      // Validate real client DNS, TLS-capable runtime libraries and non-root DB credentials.
      const dump = docker('exec', '-e', `MYSQL_PWD=${password}`, name, '/usr/bin/mysqldump', '--no-tablespaces', '--set-gtid-purged=OFF',
        '--single-transaction', '--quick', '--skip-lock-tables', '--routines', '--events', '--triggers', '--protocol=TCP', '-hdb', '-uorigin', dbname);
      assert.ok(dump.includes('CREATE TABLE `users`') && dump.includes('docker-fixture.png'), 'incomplete database dump');
      docker('stop', '-t', '40', name);
      assert.equal(JSON.parse(docker('inspect', name))[0].State.ExitCode, 0, 'shutdown was not graceful');
      docker('rm', name); resources.containers = resources.containers.filter(x => x !== name);
      launch();
      await wait(() => docker('exec', name, '/app/zhuapic', '-healthcheck'));
      api('GET', '/api/ready');
      api('GET', '/api/me', null, login.token);
      api('GET', imagePath);
      api('GET', '/install', null, null, 404);
      console.log(`${arch}: install, login, upload, original image, thumbnail, settings refresh, mysqldump, graceful stop and container recreation passed`);
    }
  } finally {
    for (const name of resources.containers.reverse()) { try { docker('rm', '-f', name); } catch {} }
    for (const name of resources.volumes) { try { docker('volume', 'rm', name); } catch {} }
    if (resources.network) { try { docker('network', 'rm', id); } catch {} }
    await fs.rm(temp, { recursive: true, force: true });
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
