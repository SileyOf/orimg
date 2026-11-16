// Explicit allowlist packaging: never include runtime configuration or user files.
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const go = process.env.GO_BIN || 'go';
  const source = await fs.readFile(path.join(root, 'config/config.go'), 'utf8');
  const version = source.match(/const AppVersion = "(v\d+\.\d+\.\d+)"/)?.[1];
  if (!version) throw new Error('Expected a three-part release version');
  const dist = path.join(root, 'dist');
  await fs.mkdir(dist, { recursive: true });
  const stage = await fs.mkdtemp(path.join(dist, '.release-'));
  const env = { ...process.env, GOTOOLCHAIN: 'local', CGO_ENABLED: '0', GOPROXY: 'off', COPYFILE_DISABLE: '1' };
  const goVersion = execFileSync(go, ['version'], { env, encoding: 'utf8' }).trim();
  const pages = ['index.html', 'admin.html', 'dashboard.html', 'register.html', 'forgot-password.html', 'account-action.html', 'install.html'];
  const assets = ['album-icons.js', 'account.css', 'styles.css', 'background.css', 'background.js', 'design.js', 'catalog-views.js',
    'design.css', 'install.css', 'branding.js', 'mail.css', 'storage-targets.js', 'logo-mark.svg', 'responsive.css', 'install.js',
    'appearance.js', 'account.js', 'appearance.css', 'account-mail.js', 'admin.js', 'mail-admin.js', 'storage-targets.css',
    'dashboard.js', 'album.js', 'logo.svg', 'album.css', 'lucide-LICENSE', 'app.js', 'favicon.svg'];
  const archives = [];
  async function copy(from, to) {
    const stat = await fs.lstat(path.join(root, from));
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`Not a regular source file: ${from}`);
    await fs.mkdir(path.dirname(to), { recursive: true });
    await fs.copyFile(path.join(root, from), to);
    await fs.chmod(to, 0o644);
  }
  async function filesIn(dir, prefix = '') {
    const result = [];
    for (const entry of await fs.readdir(dir, { withFileTypes: true })) {
      const name = path.posix.join(prefix, entry.name);
      if (entry.isDirectory()) result.push(...await filesIn(path.join(dir, entry.name), name));
      else if (entry.isFile()) result.push(name);
      else throw new Error(`Unexpected package entry: ${name}`);
    }
    return result.sort();
  }
  const digest = async (file) => crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  try {
    for (const arch of ['amd64', 'arm64']) {
      const name = `origin-${version}-upgrade-linux-${arch}`;
      const archive = path.join(dist, name + '.tar.gz');
      try { await fs.access(archive); throw new Error(`Release already exists: ${archive}`); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      const dest = path.join(stage, name);
      await fs.mkdir(dest);
      for (const page of pages) await copy(`static/${page}`, path.join(dest, 'static', page));
      for (const asset of assets) await copy(`static/assets/${asset}`, path.join(dest, 'static/assets', asset));
      await copy(`docs/UPGRADE_${version}.md`, path.join(dest, 'UPGRADE.md'));
      await copy('README_DEPLOY.md', path.join(dest, 'README_DEPLOY.md'));
      await copy('docs/INSTALLATION.md', path.join(dest, 'docs/INSTALLATION.md'));
      execFileSync(go, ['build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w', '-o', path.join(dest, 'zhuapic'), '.'],
        { cwd: root, env: { ...env, GOOS: 'linux', GOARCH: arch }, stdio: 'inherit' });
      await fs.chmod(path.join(dest, 'zhuapic'), 0o755);
      await fs.writeFile(path.join(dest, 'BUILDINFO.json'), JSON.stringify({ version, platform: `linux/${arch}`, goVersion,
        schemaVersion: 9, binaryName: 'zhuapic', builtAt: new Date().toISOString(), settingsAdded: [],
        upgradeFrom: 'v0.4.7', legacySessionKeyFile: '${INSTALL_CONFIG}.session-key',
        runtimeDataIncluded: false }, null, 2) + '\n');
      const files = await filesIn(dest);
      for (const name of files) {
        if (/(^|\/)(\.env|data|installation\.json|\.DS_Store)(\/|$)/.test(name) || /site-(logo|background)-/.test(name)) throw new Error(`Runtime file in package: ${name}`);
      }
      const sums = [];
      for (const name of files) sums.push(`${await digest(path.join(dest, name))}  ${name}`);
      await fs.writeFile(path.join(dest, 'SHA256SUMS'), sums.join('\n') + '\n');
      const temporaryArchive = path.join(stage, name + '.tar.gz');
      execFileSync('tar', ['--format=ustar', '-czf', temporaryArchive, '-C', stage, name], { env, stdio: 'inherit' });
      await fs.rename(temporaryArchive, archive);
      archives.push(`${await digest(archive)}  ${path.basename(archive)}`);
      console.log(`Packaged ${archive} (${files.length + 1} files)`);
    }
    await fs.writeFile(path.join(dist, `origin-${version}-SHA256SUMS`), archives.join('\n') + '\n');
  } finally { await fs.rm(stage, { recursive: true, force: true }); }
})().catch((error) => { console.error(error); process.exitCode = 1; });
