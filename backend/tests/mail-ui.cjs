// Browser-only fixtures: no database connections or real email delivery.
const fs=require('node:fs'),path=require('node:path'),http=require('node:http'),assert=require('node:assert/strict');
const {chromium}=require('playwright');
const root=path.resolve(__dirname,'../static'),out='/tmp/orimg-mail-qa';fs.mkdirSync(out,{recursive:true});
(async()=>{
 const server=http.createServer((req,res)=>{let pathname=new URL(req.url,'http://localhost').pathname;if(['/forgot-password','/register','/account-action','/dashboard'].includes(pathname))pathname+='.html';const file=path.resolve(root,'.'+pathname);if(!file.startsWith(root+path.sep)||!fs.existsSync(file)||!fs.statSync(file).isFile()){res.writeHead(404);res.end();return;}res.setHeader('Content-Security-Policy',"default-src 'self'; img-src 'self' data: blob: https:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'");res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)]||'application/octet-stream');fs.createReadStream(file).pipe(res);});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));const origin='http://127.0.0.1:'+server.address().port;
 const browser=await chromium.launch({channel:'chrome',headless:true});
 try{
  const context=await browser.newContext();await context.addInitScript(()=>{if(window.top!==window)return;if(!sessionStorage.getItem('mail-fixture-initialized')){localStorage.setItem('zhuapic_token','mail-ui-fixture');sessionStorage.setItem('mail-fixture-initialized','1');}});
  const user={id:1,username:'Preview',email:'preview@example.test',role:'admin',active:true,email_verified:false};
  const settings={site_name:'Origin',user_path:'/dashboard.html',admin_path:'/admin.html',db_backup_target_id:'3'};
  let config={enabled:false,host:'smtp.example.test',port:465,tls:'tls',username:'smtp-user',password:'',from:'noreply@example.test',name:'Origin',base_url:'https://img.example.com'};
  let tested=false,gate,release;const writes=[];
  const groups=['Guest','Member','Trusted','Admin'].map((name,index)=>({id:index+1,name,member_count:2,policy:{id:index+1,active:true,max_file_mb:50,max_batch_count:20,storage_mode:'default',allowed_types:'jpg,png',allow_storage_selection:true}}));
  let registration={enabled:true,group_id:2};
  await context.route('**/api/**',async route=>{
   const req=route.request(),p=new URL(req.url()).pathname,method=req.method();
   if(p.includes('/mail/templates/')) {const template=method==='GET'?{subject:'{{site_name}} · 邮件',title:'账号验证',body:'{{message}}',button:'确认操作',footer:'请勿转发链接'}:req.postDataJSON();return route.fulfill({json:{template,subject:template.subject,html:fs.readFileSync('/tmp/orimg-email-preview.html','utf8')}});}
   if(p==='/api/admin/mail/registration'){if(method==='PUT')registration=req.postDataJSON();return route.fulfill({json:registration});}
   if(p==='/api/admin/groups')return route.fulfill({json:{groups}});
   if(p==='/api/mail/status')return route.fulfill({json:{enabled:true,registration_enabled:true}});
   if(p==='/api/auth/captcha')return route.fulfill({json:{question:'4 + 5 = ?',token:'fixture-token'}});
   if(method!=='GET'){
    const body=req.postDataJSON();writes.push({p,body});if(gate)await gate;
    if(p==='/api/admin/mail/config'){config={...body,password:''};return route.fulfill({json:{saved:true}});}
    if(p==='/api/admin/mail/test')tested=true;
    return route.fulfill({json:{id:1,updated:true,confirmed:true,requires_login:true,message:'邮件已加入队列'}});
   }
   const data=p.endsWith('/public')?settings:p==='/api/admin/settings'?{settings}:p==='/api/me'?{user,upload_policy:{active:true,allow_api:false},upload_usage:{}}:p==='/api/mail/status'?{enabled:true}:p==='/api/admin/mail/config'?{config,tested,has_password:true,worker_enabled:true}:p==='/api/admin/mail/jobs'?{jobs:[{id:1,kind:'test',recipient:'p***@example.test',status:'accepted',attempts:1,error:'',created_at:new Date().toISOString()},{id:2,kind:'reset',recipient:'u***@example.test',status:'failed',attempts:5,error:'SMTP 连接失败或超时',created_at:new Date().toISOString()}],next_before:0}:p==='/api/admin/storage/targets'?{targets:[{id:3,name:'私有 NAS',provider:'local',enabled:true,configured:true,config:{directory:'/private-backups'}}]}:p==='/api/admin/storage'?{jobs:[{id:1,type:'db_backup',target_mode:'s3',status:'completed',total_items:1,success_items:0,failed_items:0,created_at:new Date().toISOString()}],stats:{}}:p==='/api/admin/groups'?{groups:[]}:p==='/api/admin/users'?{users:[user]}:{};
   return route.fulfill({json:data});
  });
  const page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(origin+'/admin.html#mail');await page.locator('#mailConfigForm [name="host"]').waitFor();await page.waitForFunction(()=>document.querySelector('#mailConfigForm [name="host"]').value==='smtp.example.test');
  assert.equal(await page.locator('#databaseBackupTarget').inputValue(),'3');
  for(const width of [390,768,1440,2560]){await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'config overflow '+width);await page.screenshot({path:out+'/config-'+width+'.png',fullPage:true});}
  await page.setViewportSize({width:1440,height:1000});
  page.once('dialog',d=>d.dismiss());await page.locator('#mailConfigForm [type="submit"]').click();assert.equal(writes.length,0,'cancel submitted settings');
  gate=new Promise(r=>release=r);page.once('dialog',d=>d.accept());await page.locator('#mailConfigForm [type="submit"]').click();assert(await page.locator('#mailConfigForm [type="submit"]').isDisabled());release();gate=null;
  await page.waitForFunction(()=>document.querySelector('#mailMessage').textContent==='邮件设置已保存');assert.equal(writes.length,1);
  await page.locator('#mailTestForm input').fill('preview@example.test');page.once('dialog',d=>d.accept());await page.locator('#mailTestForm button').click();await page.waitForFunction(()=>document.querySelector('#mailMessage').textContent.includes('测试邮件已加入'));
  await page.locator('[data-mail-tab="logs"]').click();await page.getByRole('cell',{name:'服务器已接收',exact:true}).waitFor();
  for(const width of [390,768,1440,2560]){await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'logs overflow '+width);await page.screenshot({path:out+'/logs-'+width+'.png',fullPage:true});}
  page.once('dialog',d=>d.accept());await page.locator('#mailJobs').getByRole('button',{name:'重试',exact:true}).click();await page.waitForTimeout(100);assert(writes.some(w=>w.body?.action==='retry'));
  await page.locator('[data-mail-tab="templates"]').click();await page.locator('#mailTemplateForm [name="title"]').waitFor();await page.waitForFunction(()=>document.querySelector('#mailTemplateForm [name="title"]').value==='账号验证');await page.locator('#mailTemplate').selectOption('reset');await page.frameLocator('#mailTemplatePreview').getByText('链接有效期：20 分钟。仅可使用一次。').waitFor();
  for(const width of [390,768,1440,2560]){await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'template overflow '+width);await page.screenshot({path:out+'/template-'+width+'.png',fullPage:true});}
  await page.locator('#mailTemplateForm [name="title"]').fill('欢迎回来');page.once('dialog',d=>d.accept());await page.locator('#mailTemplateForm [type="submit"]').click();await page.getByText('邮件模板已保存',{exact:true}).waitFor();
  await page.locator('[data-mail-tab="registration"]').click();await page.waitForFunction(()=>document.querySelector('#registrationSettingsForm [name="group_id"]').value==='2');await page.locator('#registrationSettingsForm [name="group_id"]').selectOption('3');page.once('dialog',d=>d.accept());await page.locator('#registrationSettingsForm [type="submit"]').click();await page.getByText('注册设置已保存',{exact:true}).waitFor();assert.equal(registration.group_id,3);
  await page.locator('.admin-nav [data-panel="groups"]').click();await page.locator('#groupViewTools [data-view="cards"]').click();await page.locator('.group-entry').first().waitFor();
  const beforeCard=await page.locator('.group-entry').first().boundingBox();await page.locator('.group-entry .group-summary').nth(1).click();await page.locator('.group-dialog[open]').waitFor();const afterCard=await page.locator('.group-entry').first().boundingBox();assert.equal(beforeCard.width,afterCard.width);assert.equal(beforeCard.height,afterCard.height);
  for(const width of [390,768,1440,2560]){await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>{const d=document.querySelector('.group-dialog'),b=d.querySelector('[type="submit"]').getBoundingClientRect();return d.scrollWidth<=d.clientWidth+1 && b.bottom<=innerHeight && b.bottom<=d.getBoundingClientRect().bottom;}),'group dialog overflow or hidden save '+width);await page.screenshot({path:out+'/group-dialog-'+width+'.png'});}
  await page.locator('.group-dialog [aria-label="关闭用户组编辑"]').click();await page.setViewportSize({width:1440,height:1000});
  await page.locator('.admin-nav [data-panel="tasks"]').click();assert.equal(await page.locator('#startMigration,#migrationDirection,.replica-mode-guide').count(),0);assert((await page.locator('#storageJobs').textContent()).includes('1 成功 · 0 失败 · 0 待处理'));
  await page.goto(origin+'/dashboard.html#account');await page.locator('#accountDialog[open]').waitFor();await page.locator('#accountMail').waitFor();assert(await page.locator('#profileForm [name="email"]').getAttribute('readonly')!==null);
  await page.locator('#verifyEmail').click();await page.waitForFunction(()=>document.querySelector('#accountMailMessage').textContent==='邮件已加入队列');
  await page.locator('#accountMail summary').click();await page.locator('#changeEmailForm [name="email"]').fill('new@example.test');await page.locator('#changeEmailForm [name="current_password"]').fill('fixture-password');await page.locator('#changeEmailForm button').click();
  await page.screenshot({path:out+'/account.png'});
  await context.clearCookies();await page.evaluate(()=>localStorage.removeItem('zhuapic_token'));await page.goto(origin+'/dashboard.html');assert.equal(await page.locator('#forgotMailForm').count(),0);await page.locator('#forgotMail').click();await page.locator('#forgotMailForm input').fill('preview@example.test');await page.locator('#forgotMailForm button').click();await page.waitForFunction(()=>document.querySelector('#forgotMailMessage').textContent==='邮件已加入队列');await page.screenshot({path:out+'/forgot.png'});
  await page.goto(origin+'/register');await page.locator('#registrationForm').waitFor();await page.locator('#registrationForm [name="email"]').fill('new@example.test');await page.locator('#registrationForm [name="captcha_answer"]').fill('9');await page.locator('#registrationForm [type="submit"]').click();await page.waitForFunction(()=>document.querySelector('#registrationMessage').textContent==='邮件已加入队列');assert(writes.some(w=>w.p==='/api/auth/register'));await page.screenshot({path:out+'/register.png'});
  await page.goto(origin+'/account-action#register:'+'b'.repeat(64));await page.locator('[name="username"]').fill('NewUser');await page.locator('[name="password"]').fill('long-fixture-password');await page.locator('[name="confirm_password"]').fill('long-fixture-password');await page.locator('#mailActionForm button').click();await page.waitForFunction(()=>document.querySelector('#mailActionForm').hidden);assert(writes.some(w=>w.p==='/api/auth/register/confirm'));
  const before=writes.length;await page.goto(origin+'/account-action.html#reset:'+'a'.repeat(64));assert.equal(new URL(page.url()).hash,'');assert.equal(writes.length,before,'confirmation ran on GET');
  await page.locator('[name="password"]').fill('long-fixture-password');await page.locator('[name="confirm_password"]').fill('different-password');await page.locator('#mailActionForm button').click();assert.equal(writes.length,before);
  await page.locator('[name="confirm_password"]').fill('long-fixture-password');await page.locator('#mailActionForm button').click();await page.waitForFunction(()=>document.querySelector('#mailActionForm').hidden);assert(writes.some(w=>w.p==='/api/auth/mail/confirm'));
  assert.deepEqual(errors,[]);console.log('PASS mail config confirmations, submit lock, test/logs/retry, HTML template preview/editing under production CSP, registration settings/flow, group dialogs, independent forgot page, email verification/change/reset, fragment privacy; four viewport widths');
 }finally{await browser.close();server.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
