(() => {
  const root = document.querySelector('[data-panel="mail"].admin-tab');
  if (!root) return;
  const $ = (s) => root.querySelector(s);
  const form = $('#mailConfigForm');
  let cursor = 0, loading = false, tab = 'config', dirty = false;
  const statuses = { queued: '等待发送', sending: '发送中', accepted: '服务器已接收', failed: '发送失败', cancelled: '已取消', expired: '已过期' };
  const kinds = { test: '测试邮件', verify: '验证邮箱', reset: '重设密码', change: '确认新邮箱', security: '安全通知',register:'注册账号' };
  const message = (text, ok = true) => { $('#mailMessage').textContent = text; $('#mailMessage').classList.toggle('error', !ok); };
  async function api(path, method = 'GET', body) {
    const response = await fetch('/api/admin/mail/' + path, { method, headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + (localStorage.getItem('zhuapic_token') || '') }, body: body === undefined ? undefined : JSON.stringify(body) });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || '请求失败');
    return data;
  }
  async function config() {
    const data = await api('config');
    if (!dirty) Object.entries(data.config).forEach(([key, value]) => {
      const field = form.elements.namedItem(key);
      if (field) field.type === 'checkbox' ? field.checked = value : field.value = value;
    });
    $('#mailTestState').textContent = !data.worker_enabled ? '后台工作器未启用' : data.tested ? '已通过发信测试' : '尚未通过发信测试';
    $('#mailTestForm button').disabled = !data.worker_enabled;
    form.elements.password.placeholder = data.has_password ? '已保存，留空保留密码' : 'SMTP 授权密码';
  }
  function cell(row, text, cls) { const node = document.createElement('td'); node.textContent = text; if (cls) node.className = cls; row.append(node); return node; }
  async function jobs(append = false) {
    const data = await api(`jobs?status=${encodeURIComponent($('#mailStatusFilter').value)}&before=${append ? cursor : 0}`);
    if (!append) {
      $('#mailJobs').replaceChildren();
      const table = document.createElement('table'); table.className = 'mail-table';
      const head = document.createElement('thead'); const row = document.createElement('tr');
      ['邮件', '收件人', '状态', '尝试', '创建时间', '操作'].forEach((title) => { const th = document.createElement('th'); th.textContent = title; row.append(th); });
      head.append(row); table.append(head, document.createElement('tbody')); $('#mailJobs').append(table);
    }
    const body = $('#mailJobs tbody');
    data.jobs.forEach((job) => {
      const row = document.createElement('tr');
      cell(row, kinds[job.kind] || job.kind); cell(row, job.recipient);
      const state = cell(row, statuses[job.status] || job.status);
      if (job.error) { const small = document.createElement('small'); small.textContent = job.error; state.append(small); }
      cell(row, String(job.attempts)); cell(row, new Date(job.created_at).toLocaleString('zh-CN'));
      const actions = cell(row, '');
      (job.status === 'failed' ? ['retry', 'cancel'] : job.status === 'queued' ? ['cancel'] : []).forEach((action) => {
        const button = document.createElement('button'); button.type = 'button'; button.className = 'ghost-button compact'; button.textContent = action === 'retry' ? '重试' : '取消';
        button.onclick = async () => {
          if (!confirm(`${button.textContent}这封邮件？`)) return;
          button.disabled = true;
          try { await api(`jobs/${job.id}`, 'POST', { action }); await jobs(); } catch (error) { message(error.message, false); button.disabled = false; }
        }; actions.append(button);
      }); body.append(row);
    });
    cursor = data.next_before; $('#mailMore').hidden = !cursor;
    if (!body.children.length) { const row = document.createElement('tr'); const td = cell(row, '暂无发送记录'); td.colSpan = 6; body.append(row); }
  }
  async function load() {
    if (loading) return; loading = true; $('#mailRefresh').disabled = true;
    try { await config(); if (tab === 'logs') await jobs(); if(tab==='templates'&&!templateLoaded) await loadTemplate(); if(tab==='registration'&&!registrationLoaded) await loadRegistration(); } catch (error) { message(error.message, false); }
    finally { loading = false; $('#mailRefresh').disabled = false; }
  }
  form.addEventListener('input', () => { dirty = true; });
  form.onsubmit = async (event) => {
    event.preventDefault(); const button = form.querySelector('button[type="submit"]');
    if (button.disabled || !confirm('保存邮件配置？连接信息变化后需要重新测试，旧配置下的待发邮件会取消。')) return;
    button.disabled = true;
    const data = Object.fromEntries(new FormData(form)); data.enabled = form.elements.enabled.checked; data.port = Number(data.port);
    try { await api('config', 'PUT', data); dirty = false; await config(); message('邮件设置已保存'); }
    catch (error) { message(error.message, false); } finally { button.disabled = false; }
  };
  $('#mailTestForm').onsubmit = async (event) => {
    event.preventDefault(); if (dirty) { message('请先保存配置，再发送测试邮件', false); return; }
    const button = event.currentTarget.querySelector('button');
    if (button.disabled || !confirm('向填写的邮箱发送一封测试邮件？')) return;
    button.disabled = true;
    try { await api('test', 'POST', { email: event.currentTarget.elements.email.value }); message('测试邮件已加入队列，请查看发送记录'); }
    catch (error) { message(error.message, false); } finally { button.disabled = false; }
  };
  root.querySelectorAll('[data-mail-tab]').forEach((button) => button.onclick = () => {
    tab = button.dataset.mailTab;
    root.querySelectorAll('[data-mail-tab]').forEach((el) => el.setAttribute('aria-selected', String(el === button)));
    root.querySelectorAll('[data-mail-view]').forEach((el) => el.hidden = el.dataset.mailView !== tab);
    load();
  });
  $('#mailRefresh').onclick = load;
  $('#mailStatusFilter').onchange = () => jobs().catch((e) => message(e.message, false));
  $('#mailMore').onclick = async () => { $('#mailMore').disabled = true; try { await jobs(true); } catch (e) { message(e.message, false); } finally { $('#mailMore').disabled = false; } };
  const templateForm=$('#mailTemplateForm'),registrationForm=$('#registrationSettingsForm');
  let templateLoaded=false,templateDirty=false,templateKind='verify',templateBusy=false,registrationLoaded=false;
  function renderPreview(data) {$('#mailTemplatePreview').srcdoc=data.html;$('#mailPreviewSubject').textContent=data.subject;}
  async function loadTemplate() {
    const kind=$('#mailTemplate').value;
    const data=await api('templates/'+kind);
    Object.entries(data.template).forEach(([key,value])=>{const field=templateForm.elements.namedItem(key);if(field)field.value=value;});
    templateForm.elements.button.closest('label').hidden=kind==='security'||kind==='test';
    templateKind=kind;templateLoaded=true;templateDirty=false;renderPreview(data);$('#mailTemplateMessage').textContent='';
  }
  templateForm.oninput=()=>{templateDirty=true;};
  $('#mailTemplate').onchange=async()=>{
    if(templateDirty&&!confirm('放弃当前模板的未保存修改？')){$('#mailTemplate').value=templateKind;return;}
    try {await loadTemplate();} catch(error){$('#mailTemplate').value=templateKind;message(error.message,false);}
  };
  async function updateTemplate(save) {
    if(templateBusy||!templateForm.reportValidity())return;
    if(save&&!confirm('保存此邮件模板？更改仅应用于之后新发送的邮件。'))return;
    const body=Object.fromEntries(new FormData(templateForm));
    templateBusy=true;const controls=[...templateForm.elements,$('#mailTemplate')];controls.forEach(el=>el.disabled=true);
    try {const data=await api('templates/'+templateKind,save?'PUT':'POST',body);renderPreview(data);if(save)templateDirty=false;$('#mailTemplateMessage').textContent=save?'邮件模板已保存':'预览已更新';}
    catch(error){$('#mailTemplateMessage').textContent=error.message;}
    finally{templateBusy=false;controls.forEach(el=>el.disabled=false);}
  }
  templateForm.onsubmit=event=>{event.preventDefault();updateTemplate(true);};
  $('#mailPreviewButton').onclick=()=>updateTemplate(false);
  async function loadRegistration() {
    const response=await fetch('/api/admin/groups',{headers:{Authorization:'Bearer '+(localStorage.getItem('zhuapic_token')||'')}});
    if(!response.ok)throw new Error('用户组读取失败');
    const data=await response.json(),settings=await api('registration');
    const select=registrationForm.elements.group_id;select.replaceChildren(new Option('请选择用户组','0'));
    (data.groups||[]).forEach(group=>{const option=new Option(group.name+(group.policy?.active?'':'（已停用）'),String(group.id));select.append(option);});
    if(settings.group_id&&!Array.from(select.options).some(o=>Number(o.value)===settings.group_id))select.append(new Option('原用户组已删除',String(settings.group_id)));
    select.value=String(settings.group_id);registrationForm.elements.enabled.checked=settings.enabled;registrationLoaded=true;
  }
  registrationForm.onsubmit=async event=>{
    event.preventDefault();const button=registrationForm.querySelector('[type="submit"]');if(button.disabled||!confirm('保存邮箱注册设置？新账号将加入所选用户组。'))return;
    const body={enabled:registrationForm.elements.enabled.checked,group_id:Number(registrationForm.elements.group_id.value)};
    button.disabled=true;
    try{await api('registration','PUT',body);$('#registrationSettingsMessage').textContent='注册设置已保存';}
    catch(error){$('#registrationSettingsMessage').textContent=error.message;}
    finally{button.disabled=false;}
  };
  window.addEventListener('beforeunload',event=>{if(dirty||templateDirty){event.preventDefault();event.returnValue='';}});
  setInterval(() => { if (!root.hidden && !document.hidden && !dirty) load(); }, 10000);
  window.OrimgMail = { load };
})();
