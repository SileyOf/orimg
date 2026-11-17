// Binary release packages for Linux amd64/arm64, usable for both new installs
// and upgrades. The binaries are copied out of the multi-arch Docker image so
// both distribution channels ship the exact same build (pinned Go toolchain).
//
//   docker buildx build --platform linux/amd64,linux/arm64 --tag yuki955/orimg:X.Y.Z --load .
//   node release/package.cjs
//
// Explicit allowlist: never include runtime configuration or user files.
const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const source = await fs.readFile(path.join(root, 'config/config.go'), 'utf8');
  const version = source.match(/const AppVersion = "v(\d+\.\d+\.\d+)"/)?.[1];
  if (!version) throw new Error('Expected a three-part release version');
  const image = process.env.ORIGIN_IMAGE || `yuki955/orimg:${version}`;
  const dist = path.join(root, 'dist');
  await fs.mkdir(dist, { recursive: true });
  const stage = await fs.mkdtemp(path.join(dist, '.release-'));
  const env = { ...process.env, COPYFILE_DISABLE: '1' };
  // Every tracked static file except runtime-generated site assets.
  const staticFiles = execFileSync('git', ['ls-files', 'static'], { cwd: root, encoding: 'utf8' }).split('\n')
    .filter(f => f && !/(^|\/)(\.DS_Store|site-(logo|background)-[^/]*)$/.test(f));
  const extras = [
    ['.env.example', '.env.example'], ['deploy/origin.service', 'origin.service'],
    ['docs/BINARY.md', 'README.md'], [`docs/UPGRADE_v${version}.md`, 'UPGRADE.md'],
    [path.join('..', 'LICENSE'), 'LICENSE'], [path.join('..', 'THIRD_PARTY_NOTICES.md'), 'THIRD_PARTY_NOTICES.md'],
  ];
  async function copy(from, to, mode = 0o644) {
    const stat = await fs.lstat(path.join(root, from));
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`Not a regular source file: ${from}`);
    await fs.mkdir(path.dirname(to), { recursive: true });
    await fs.copyFile(path.join(root, from), to);
    await fs.chmod(to, mode);
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
  const digest = async file => crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  const archives = [];
  try {
    for (const arch of ['amd64', 'arm64']) {
      const name = `origin-v${version}-linux-${arch}`;
      const archive = path.join(dist, name + '.tar.gz');
      try { await fs.access(archive); throw new Error(`Release already exists: ${archive}`); }
      catch (error) { if (error.code !== 'ENOENT') throw error; }
      const dest = path.join(stage, name);
      await fs.mkdir(dest);
      for (const file of staticFiles) await copy(file, path.join(dest, file));
      for (const [from, to] of extras) await copy(from, path.join(dest, to));
      const container = execFileSync('docker', ['create', '--platform', `linux/${arch}`, image], { encoding: 'utf8' }).trim();
      try {
        execFileSync('docker', ['cp', `${container}:/app/zhuapic`, path.join(dest, 'zhuapic')], { stdio: 'inherit' });
      } finally { execFileSync('docker', ['rm', container], { stdio: 'ignore' }); }
      await fs.chmod(path.join(dest, 'zhuapic'), 0o755);
      const kind = execFileSync('file', ['-b', path.join(dest, 'zhuapic')], { encoding: 'utf8' });
      if (!kind.includes(arch === 'amd64' ? 'x86-64' : 'aarch64')) throw new Error(`Unexpected binary for ${arch}: ${kind}`);
      await fs.writeFile(path.join(dest, 'BUILDINFO.json'), JSON.stringify({ version: `v${version}`, platform: `linux/${arch}`,
        image, schemaVersion: 9, binaryName: 'zhuapic', builtAt: new Date().toISOString(), runtimeDataIncluded: false }, null, 2) + '\n');
      const files = await filesIn(dest);
      for (const file of files) {
        if (/(^|\/)(\.env|data|installation\.json|\.DS_Store)(\/|$)/.test(file) || /site-(logo|background)-/.test(file)) throw new Error(`Runtime file in package: ${file}`);
      }
      const sums = [];
      for (const file of files) sums.push(`${await digest(path.join(dest, file))}  ${file}`);
      await fs.writeFile(path.join(dest, 'SHA256SUMS'), sums.join('\n') + '\n');
      const temporaryArchive = path.join(stage, name + '.tar.gz');
      execFileSync('tar', ['--format=ustar', '-czf', temporaryArchive, '-C', stage, name], { env, stdio: 'inherit' });
      await fs.rename(temporaryArchive, archive);
      archives.push(`${await digest(archive)}  ${path.basename(archive)}`);
      console.log(`Packaged ${archive} (${files.length + 1} files)`);
    }
    await fs.writeFile(path.join(dist, `origin-v${version}-SHA256SUMS`), archives.join('\n') + '\n');
  } finally { await fs.rm(stage, { recursive: true, force: true }); }
})().catch(error => { console.error(error); process.exitCode = 1; });
