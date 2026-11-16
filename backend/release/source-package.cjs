#!/usr/bin/env node
'use strict';

const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');

const root = path.resolve(__dirname, '../..');
const version = fs.readFileSync(path.join(root, 'backend/config/config.go'), 'utf8')
  .match(/const AppVersion = "(v\d+\.\d+\.\d+)"/)?.[1];
if (!version) throw new Error('Expected a three-part application version');
const date = process.env.SOURCE_PACKAGE_DATE || new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
}).format(new Date()).replaceAll('-', '');
if (!/^\d{8}$/.test(date)) throw new Error('SOURCE_PACKAGE_DATE must be YYYYMMDD');
const name = `origin-${version}-source-${date}`;
const dist = path.join(root, 'backend/dist');
fs.mkdirSync(dist, { recursive: true });
const archive = path.join(dist, `${name}.zip`);
if (fs.existsSync(archive) || fs.existsSync(`${archive}.sha256`)) {
  throw new Error('Source archive already exists; refusing to overwrite it');
}

const files = [];
const excluded = /^(?:\.DS_Store|\.git|node_modules|\.gocache|test-results|dist|data|installation\.json.*|.*\.session-key|\.session-key-.*|site-logo-.*|site-background-.*)$/;
const extensions = new Set(['.go', '.mod', '.sum', '.md', '.cjs', '.js', '.css', '.html', '.svg', '.sh', '.yaml', '.yml', '.json', '.example']);
function collect(relative, explicit = false) {
  const full = path.join(root, relative);
  const stat = fs.lstatSync(full);
  const base = path.basename(relative);
  if (stat.isSymbolicLink()) throw new Error(`Symlink not allowed: ${relative}`);
  if (excluded.test(base) || (base.startsWith('.env') && base !== '.env.example')) return;
  if (stat.isDirectory()) {
    for (const child of fs.readdirSync(full).sort()) collect(`${relative}/${child}`);
  } else if (stat.isFile()) {
    if (!explicit && !extensions.has(path.extname(base)) && !base.endsWith('LICENSE')) {
      throw new Error(`Review unexpected source file before packaging: ${relative}`);
    }
    const bytes = fs.readFileSync(full);
    if (/-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/.test(bytes.toString('utf8'))) {
      throw new Error(`Private key material found in ${relative}`);
    }
    files.push({ path: relative, bytes, mode: base.endsWith('.sh') ? 0o755 : 0o644 });
  } else {
    throw new Error(`Unsupported file type: ${relative}`);
  }
}

for (const file of ['README.md', 'CHANGELOG.md', 'CONTRIBUTING.md', 'SECURITY.md', 'THIRD_PARTY_NOTICES.md', '.gitignore', '.dockerignore']) collect(file, true);
collect('licenses');
for (const file of ['main.go', 'healthcheck.go', 'healthcheck_test.go', 'go.mod', 'go.sum', 'Dockerfile', '.env.example', '.dockerignore', 'README_DEPLOY.md', 'README_DEPLOY_HISTORY.md']) collect(`backend/${file}`, true);
for (const dir of ['config', 'database', 'handlers', 'installer', 'internal', 'mailer', 'middleware', 'models', 'routes', 'storage', 'utils', 'static', 'tests', 'docker', 'release', 'docs']) collect(`backend/${dir}`);
files.sort((a, b) => a.path.localeCompare(b.path, 'en'));
const digest = bytes => crypto.createHash('sha256').update(bytes).digest('hex');
const manifest = {
  version, snapshotDate: date, type: 'source-only',
  license: 'Project license has not been selected; see THIRD_PARTY_NOTICES.md.',
  exclusions: ['production configuration and credentials', 'runtime data and uploads', 'build and test outputs', 'dependencies and container images'],
  files: files.map(file => ({ path: file.path, size: file.bytes.length, sha256: digest(file.bytes) })),
};
files.push({ path: 'SOURCE_MANIFEST.json', bytes: Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`), mode: 0o644 });
const sums = files.map(file => `${digest(file.bytes)}  ${file.path}`).join('\n') + '\n';
files.push({ path: 'SHA256SUMS', bytes: Buffer.from(sums), mode: 0o644 });
const staging = fs.mkdtempSync(path.join(dist, '.source-stage-'));
try {
  for (const file of files) {
    const target = path.join(staging, name, file.path);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, file.bytes, { mode: file.mode });
  }
  execFileSync('zip', ['-X', '-q', '-r', archive, name], { cwd: staging, stdio: 'inherit' });
  execFileSync('unzip', ['-tq', archive], { stdio: 'inherit' });
  fs.writeFileSync(`${archive}.sha256`, `${digest(fs.readFileSync(archive))}  ${path.basename(archive)}\n`, { flag: 'wx' });
  console.log(JSON.stringify({ archive, checksum: `${archive}.sha256`, files: files.length, bytes: fs.statSync(archive).size }, null, 2));
} catch (error) {
  fs.rmSync(archive, { force: true });
  throw error;
} finally {
  fs.rmSync(staging, { recursive: true, force: true });
}
