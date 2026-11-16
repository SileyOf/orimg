(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const access = $('accessForm'), database = $('databaseForm'), account = $('accountForm');
  let token = '', connection = null, existing = false, pending = false, busy = false;
  database.elements.base_url.value = location.origin;
  function message(text, error = false) { $('installMessage').textContent = text; $('installMessage').dataset.error = String(error); }
  function step(index) {
    ['accessStep', 'databaseStep', 'accountStep', 'successStep'].forEach((id, i) => { $(id).hidden = i !== index; });
    document.querySelectorAll('.install-steps li').forEach((li, i) => { if (i === index) li.setAttribute('aria-current', 'step'); else li.removeAttribute('aria-current'); });
    message('');
    const heading = document.querySelector('main section:not([hidden]) h1');
    heading.tabIndex = -1; heading.focus({preventScroll:true});
  }
  async function api(path, body) {
    const response = await fetch('/api/install/' + path, {method:body ? 'POST' : 'GET', cache:'no-store', credentials:'omit', headers:{'Authorization':'Bearer ' + token, 'Content-Type':'application/json'}, ...(body ? {body:JSON.stringify(body)} : {})});
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.error || data.message || '操作失败，请检查服务状态后重试');
    return data;
  }
  async function run(form, action) {
    if (busy) return; busy = true;
    const buttons = [...document.querySelectorAll('button')]; buttons.forEach((button) => { button.disabled = true; });
    form.setAttribute('aria-busy', 'true');
    try { await action(); } catch (error) { message(error.message, true); }
    finally { busy = false; buttons.forEach((button) => { button.disabled = false; }); form.removeAttribute('aria-busy'); }
  }
  access.addEventListener('submit', (event) => { event.preventDefault(); run(access, async () => {
    token = access.elements.token.value.trim(); message('正在验证安装权限…');
    const status = await api('status'); pending = status.pending;
    access.reset(); step(1); if (pending) message('检测到未完成的安装，请使用原数据库继续。');
  }); });
  database.addEventListener('submit', (event) => { event.preventDefault(); run(database, async () => {
    const body = Object.fromEntries(new FormData(database)); message('正在检查数据库连接…');
    const result = await api('check', body); connection = body; existing = !result.bootstrap; pending = result.resume;
    $('accountHeading').textContent = existing ? '确认已有站点' : '设置管理员';
    $('databaseSummary').textContent = existing ? `已有数据库 · ${result.database.users} 位用户 · ${result.database.images} 张图片 · 结构版本 ${result.database.version}` : '空数据库 · 创建首位管理员并绑定管理员用户组';
    $('usernameField').hidden = existing; account.elements.admin_username.required = !existing;
    $('emailLabel').textContent = existing ? '原管理员邮箱' : '管理员邮箱';
    $('passwordLabel').textContent = existing ? '原管理员密码' : '管理员密码（至少 12 位）';
    account.elements.admin_password.minLength = existing ? 1 : 12;
    account.elements.admin_password.autocomplete = existing ? 'current-password' : 'new-password';
    $('confirmPasswordField').hidden = existing; account.elements.confirm_password.required = !existing;
    $('originalSecretField').hidden = !existing || pending; account.elements.original_secret.required = existing && !pending;
    ['existingNotice', 'workersField', 'workersNote'].forEach((id) => { $(id).hidden = !existing; });
    $('completeButton').textContent = existing ? '连接并升级' : '完成安装';
    step(2);
  }); });
  $('backToDatabase').addEventListener('click', () => { if (!busy) { connection = null; step(1); } });
  account.addEventListener('submit', (event) => { event.preventDefault(); run(account, async () => {
    if (!connection) throw new Error('请先检查数据库连接');
    const values = Object.fromEntries(new FormData(account));
    if (!existing && values.admin_password !== values.confirm_password) throw new Error('两次密码不一致');
    if (!confirm(existing ? '确认已备份数据库并准备好原存储目录？将保留旧账号和配置，升级缺失的结构。' : '确认初始化这个数据库并创建管理员？')) return;
    delete values.confirm_password;
    message(existing ? '正在校验旧账号、密钥并升级数据库，请勿关闭页面…' : '正在初始化数据库，请勿关闭页面…');
    const result = await api('complete', {...connection, ...values, existing, confirmed:account.elements.confirmed.checked, enable_workers:account.elements.enable_workers.checked});
    token = ''; connection = null; database.reset(); account.reset(); step(3);
    $('successNote').textContent = result.workers_paused ? '后台任务已暂停。保管 data/installation.json，其中包含数据库连接信息和原密钥。' : '请保管 data/installation.json，并将配置和图片目录一起备份。';
    $('loginLink').href = '/dashboard';
  }); });
})();
