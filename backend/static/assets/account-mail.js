(() => {
  let available = false;
  let currentUser;
  const $ = (s) => document.querySelector(s);
  if ($('.mail-action-shell') && window.ZhuaBranding) window.ZhuaBranding.load().catch(()=>{delete document.body.dataset.brandPending;});
  async function api(path, body) {
    const token = localStorage.getItem('zhuapic_token');
    const response = await fetch(path, { method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) }, body: body ? JSON.stringify(body) : undefined });
    const data = await response.json(); if (!response.ok) throw new Error(data.error || '请求失败'); return data;
  }
  function refresh(user) {
    currentUser = user;
    if (!$('#accountMail')) return;
    $('#accountMail').hidden = !available || !user;
    $('#emailVerifiedState').textContent = user?.email_verified ? '当前邮箱已验证' : '当前邮箱尚未验证';
    $('#verifyEmail').hidden = Boolean(user?.email_verified);
  }
  async function send(button, purpose, body, output) {
    if (button.disabled) return;
    button.disabled = true;
    try { const data = await api(`/api/auth/mail/${purpose}`, body); output.textContent = data.message; }
    catch (error) { output.textContent = error.message; }
    finally { button.disabled = false; }
  }
  $('#verifyEmail')?.addEventListener('click', (event) => send(event.currentTarget, 'verify', {}, $('#accountMailMessage')));
  $('#changeEmailForm')?.addEventListener('submit', (event) => {
    event.preventDefault(); const form = event.currentTarget;
    send(form.querySelector('button'), 'change', Object.fromEntries(new FormData(form)), $('#accountMailMessage')).then(() => form.elements.current_password.value = '');
  });
  $('#forgotMailForm')?.addEventListener('submit', (event) => {
    event.preventDefault(); const form = event.currentTarget;
    send(form.querySelector('button'), 'reset', { email: form.elements.email.value }, $('#forgotMailMessage'));
  });
  let captchaToken='';
  async function registrationCaptcha() {
    const button=$('#registrationCaptcha');button.disabled=true;
    try {const data=await api('/api/auth/captcha');captchaToken=data.token;button.textContent=data.question;}
    catch(error) {captchaToken='';$('#registrationMessage').textContent=error.message;button.textContent='重新加载验证码';}
    finally {button.disabled=false;}
  }
  $('#registrationCaptcha')?.addEventListener('click',registrationCaptcha);
  $('#registrationForm')?.addEventListener('submit',async event=>{
    event.preventDefault();const form=event.currentTarget,button=form.querySelector('[type="submit"]');if(button.disabled)return;
    button.disabled=true;
    try {const data=await api('/api/auth/register',{email:form.elements.email.value,captcha_token:captchaToken,captcha_answer:form.elements.captcha_answer.value});$('#registrationMessage').textContent=data.message;}
    catch(error) {$('#registrationMessage').textContent=error.message;}
    finally {form.elements.captcha_answer.value='';await registrationCaptcha();button.disabled=false;}
  });
  if ($('#accountMail') || $('#forgotMail') || $('#forgotMailForm') || $('#registrationForm')) api('/api/mail/status').then(async data => {
    available=data.enabled;
    if($('#forgotMail'))$('#forgotMail').hidden=!available;
    if($('#registerLink'))$('#registerLink').hidden=!data.registration_enabled;
    if($('#forgotMailForm')) {$('#forgotMailForm button').disabled=!available;$('#forgotMailMessage').textContent=available?'':'邮件服务尚未启用，请联系管理员。';}
    if($('#registrationForm')) {$('#registrationForm').hidden=!data.registration_enabled;$('#registrationMessage').textContent=data.registration_enabled?'':'站点暂未开放注册。';if(data.registration_enabled) await registrationCaptcha();}
    refresh(currentUser);
  }).catch(()=>{if($('#forgotMailMessage'))$('#forgotMailMessage').textContent='无法连接邮件服务，请稍后刷新。';if($('#registrationMessage'))$('#registrationMessage').textContent='无法读取注册状态，请稍后刷新。';});
  window.OrimgAccountMail = { refresh };
  const actionForm = $('#mailActionForm');
  if (!actionForm) return;
  const match = location.hash.match(/^#(reset|verify|change|register):([a-f0-9]{64})$/);
  history.replaceState(null, '', location.pathname);
  const output = $('#mailActionMessage');
  if (!match) { actionForm.hidden = true; output.textContent = '验证链接无效，请重新申请。'; return; }
  const [, purpose, token] = match;
  $('#mailActionTitle').textContent = ({ reset: '重设密码', verify: '验证邮箱', change: '确认新邮箱', register:'设置账号' })[purpose];
  const needsPassword=purpose==='reset'||purpose==='register';
  $('#mailPasswordFields').hidden=!needsPassword;
  $('#registrationName').hidden=purpose!=='register';actionForm.elements.username.required=purpose==='register';
  actionForm.elements.password.required=needsPassword;actionForm.elements.confirm_password.required=needsPassword;
  actionForm.onsubmit = async (event) => {
    event.preventDefault(); const button = actionForm.querySelector('button');
    if (button.disabled) return;
    if (needsPassword && actionForm.elements.password.value !== actionForm.elements.confirm_password.value) { output.textContent = '两次密码不一致'; return; }
    button.disabled = true;
    try {
      const data = await api(purpose==='register'?'/api/auth/register/confirm':'/api/auth/mail/confirm', { purpose, token, username:actionForm.elements.username.value,password: needsPassword ? actionForm.elements.password.value : '' });
      if (data.requires_login) localStorage.removeItem('zhuapic_token');
      actionForm.reset(); actionForm.hidden = true;
      output.textContent = purpose==='register'?'账号已创建，邮箱已验证。现在可以登录了。':data.requires_login ? '账号已更新，请使用新信息重新登录。' : '邮箱验证完成。';
    } catch (error) { output.textContent = error.message; } finally { button.disabled = false; }
  };
})();
