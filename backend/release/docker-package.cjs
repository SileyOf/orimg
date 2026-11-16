const fs = require('node:fs/promises');
const path = require('node:path');
const crypto = require('node:crypto');
const { execFileSync } = require('node:child_process');

(async () => {
  const root = path.resolve(__dirname, '..');
  const source = await fs.readFile(path.join(root, 'config/config.go'), 'utf8');
  const version = source.match(/const AppVersion = "v(\d+\.\d+\.\d+)"/)?.[1];
  if (!version) throw Error('Invalid release version');
  const image = `origin:${version}`;
  const dist = path.join(root, 'dist');
  await fs.mkdir(dist, { recursive: true });
  const stage = await fs.mkdtemp(path.join(dist, '.docker-release-'));
  const digest = async file => crypto.createHash('sha256').update(await fs.readFile(file)).digest('hex');
  const sums = [];
  try {
    for (const arch of ['amd64', 'arm64']) {
      const name = `origin-v${version}-docker-linux-${arch}`;
      const archive = path.join(dist, `${name}.tar.gz`);
      try { await fs.access(archive); throw Error(`Already exists: ${archive}`); }
      catch (e) { if (e.code !== 'ENOENT') throw e; }
      const dest = path.join(stage, name);
      await fs.mkdir(dest);
      for (const [from, to] of [['docker/compose.yaml','compose.yaml'], ['docker/compose.mysql.yaml','compose.mysql.yaml'],
        ['docker/compose.legacy.yaml.example','compose.legacy.yaml.example'], ['docker/.env.example','.env.example'], ['docs/DOCKER.md','README.md']]) {
        await fs.copyFile(path.join(root, from), path.join(dest, to));
      }
      execFileSync('docker', ['image', 'save', '--platform', `linux/${arch}`, '-o', path.join(dest, 'origin-image.tar'), image], { stdio: 'inherit' });
      const info = JSON.parse(execFileSync('docker', ['image', 'inspect', '--platform', `linux/${arch}`, image], { encoding:'utf8' }))[0];
      if (info.Config.User !== '10001:10001' || info.Config.Labels['org.opencontainers.image.version'] !== version) throw Error('Unexpected image configuration');
      await fs.writeFile(path.join(dest, 'BUILDINFO.json'), JSON.stringify({ version, image, platform:`linux/${arch}`, imageID:info.Id,
        schemaVersion:9, builtAt:new Date().toISOString(), includesDatabaseServer:false, includesUserData:false }, null, 2)+'\n');
      const files = (await fs.readdir(dest)).sort();
      const hashes = [];
      for (const file of files) hashes.push(`${await digest(path.join(dest, file))}  ${file}`);
      await fs.writeFile(path.join(dest, 'SHA256SUMS'), hashes.join('\n')+'\n');
      const tempArchive = path.join(stage, `${name}.tar.gz`);
      execFileSync('tar', ['--format=ustar','-czf',tempArchive,'-C',stage,name], {env:{...process.env,COPYFILE_DISABLE:'1'},stdio:'inherit'});
      await fs.rename(tempArchive, archive);
      sums.push(`${await digest(archive)}  ${path.basename(archive)}`);
      console.log(`Packaged ${archive}`);
    }
    await fs.writeFile(path.join(dist, `origin-v${version}-docker-SHA256SUMS`), sums.join('\n')+'\n');
  } finally { await fs.rm(stage, { recursive:true, force:true }); }
})().catch(e => { console.error(e); process.exitCode=1; });
