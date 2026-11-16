// Fully isolated browser fixtures. No database or outbound mail/storage connections.
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const sharp = require('sharp');
const root = path.resolve(__dirname, '../static');
const out = '/tmp/orimg-profile-qa'; fs.mkdirSync(out, {recursive:true});
(async () => {
  const avatar = await sharp({create:{width:400,height:300,channels:3,background:'#497cad'}}).png().toBuffer();
  const server = http.createServer((req,res) => {
    const file = path.resolve(root, '.'+new URL(req.url,'http://localhost').pathname);
    if (!file.startsWith(root+path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {res.writeHead(404);res.end();return;}
    res.setHeader('Content-Type', ({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[path.extname(file)] || 'application/octet-stream');
    fs.createReadStream(file).pipe(res);
  });
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
  const origin='http://127.0.0.1:'+server.address().port;
  const browser=await chromium.launch({channel:'chrome',headless:true});
  try {
    const context=await browser.newContext();
    await context.addInitScript(()=>localStorage.setItem('zhuapic_token','isolated-profile-fixture'));
    const user={id:1,username:'Preview',email:'preview@example.test',role:'admin',group_id:1,group_name:'创作者',active:true,quota_bytes:null,avatar_url:''};
    const group={id:1,name:'创作者',slug:'old-system',is_system:true,member_count:1,policy:{id:1,active:true,storage_mode:'target:1',allow_storage_selection:false,max_file_mb:50,max_batch_count:20,total_quota_bytes:1073741824,allowed_types:'png,jpg'}};
    const settings={site_name:'Origin',hero_title:'Origin',storage_mode:'local',user_path:'/dashboard.html',admin_path:'/admin.html'};
    const writes=[]; let guestID=0;
    await context.route('**/avatars/**',route=>route.fulfill({body:avatar,contentType:'image/png'}));
    await context.route('**/api/**', async route => {
      const req=route.request(),p=new URL(req.url()).pathname;
      if(p==='/api/me/avatar') {
        writes.push({p,method:req.method()});
        user.avatar_url=req.method()==='POST'?'/avatars/'+'a'.repeat(32)+'.png':'';
        return route.fulfill({json:{avatar_url:user.avatar_url}});
      }
      if(req.method()!=='GET') {
        const body=req.postDataJSON();writes.push({p,body});
        if(p==='/api/admin/policies/1')Object.assign(group.policy,body);
        if(p==='/api/admin/users/1')Object.assign(user,body);
        if(p==='/api/admin/guest-group')guestID=body.group_id;
        return route.fulfill({json:{updated:true}});
      }
      const data=p.endsWith('/public')?settings:p==='/api/admin/settings'?{settings}:p==='/api/me'?{user,upload_policy:group.policy,upload_usage:{total_bytes:1048576}}:p==='/api/admin/groups'?{groups:[group],guest_group_id:guestID}:p==='/api/admin/users'?{users:[user]}:p==='/api/admin/storage/targets'?{targets:[{id:1,name:'本地',provider:'local',enabled:true,configured:true,config:{directory:'/data/uploads'}}]}:{};
      return route.fulfill({json:data});
    });
    const page=await context.newPage(),errors=[]; page.on('pageerror',err=>errors.push(err.message));
    await page.goto(origin+'/admin.html#groups'); await page.locator('.group-entry .group-summary').click();
    await page.locator('.policy-card').waitFor();
    assert.equal(await page.getByText('内置组',{exact:true}).count(),0);
    assert(await page.locator('[data-delete-group="1"]').isVisible(),'formerly built-in group cannot be deleted');
    const form=page.locator('.policy-card');
    assert.equal(await form.locator('[name="storage_mode"]').inputValue(),'target:1');
    assert.equal(await form.locator('[name="allow_storage_selection"]').isChecked(),false);
    await form.locator('[name="allow_storage_selection"]').check();
    page.once('dialog',d=>d.accept());await form.locator('[type="submit"]').click();
    await page.waitForFunction(()=>document.querySelector('.group-name small').textContent.includes('可自选'));
    assert.equal(writes.find(w=>w.p==='/api/admin/policies/1').body.allow_storage_selection,true);
    await page.locator('#guestGroupSelect').selectOption('1');page.once('dialog',d=>d.accept());await page.locator('#guestGroupForm [type="submit"]').click();
    await page.waitForFunction(()=>document.querySelector('.group-name small').textContent.includes('游客'));
    await page.locator('.group-entry .group-summary').click();
    for(const width of [390,768,1440,2560]){
      await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'group overflow '+width);
      await page.screenshot({path:out+'/groups-'+width+'.png',fullPage:true});
    }
    await page.locator('.group-dialog [aria-label="关闭用户组编辑"]').click();
    await page.locator('.admin-nav [data-panel="users"]').click();await page.locator('[data-edit-user="1"]').click();
    await page.locator('#userEditor [name="quota_mode"]').selectOption('custom');
    await page.locator('#userEditor [name="quota_gb"]').fill('12.5');
    for(const width of [390,768,1440,2560]){
      await page.setViewportSize({width,height:1000});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'user editor overflow '+width);
      await page.screenshot({path:out+'/user-'+width+'.png'});
    }
    page.once('dialog',d=>d.accept());await page.locator('#userEditor [type="submit"]').click();
    await page.waitForFunction(()=>document.querySelector('#userEditor').hidden);
    assert.equal(writes.find(w=>w.p==='/api/admin/users/1').body.quota_bytes,12.5*1073741824);
    await page.locator('[data-edit-user="1"]').click();
    await page.locator('#userEditor [name="quota_mode"]').selectOption('inherit');
    assert(await page.locator('#userEditor [name="quota_gb"]').isDisabled());
    page.once('dialog',d=>d.accept());await page.locator('#userEditor [type="submit"]').click();
    await page.waitForFunction(()=>document.querySelector('#userEditor').hidden);
    assert.equal(writes.filter(w=>w.p==='/api/admin/users/1').at(-1).body.quota_bytes,null);
    await page.goto(origin+'/dashboard.html#account'); await page.locator('#accountDialog[open]').waitFor();
    await page.locator('#avatarFile').setInputFiles({name:'avatar.png',mimeType:'image/png',buffer:avatar});
    await page.waitForFunction(()=>document.querySelector('#profileMessage').textContent==='头像已更新');
    assert.equal(await page.locator('.account-avatar img').count(),1);
    for(const width of [390,768,1440]){
      await page.setViewportSize({width,height:900});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'profile overflow '+width);
      await page.screenshot({path:out+'/avatar-'+width+'.png'});
    }
    page.once('dialog',d=>d.accept());await page.locator('#removeAvatar').click();
    await page.waitForFunction(()=>document.querySelector('#profileMessage').textContent==='头像已移除');
    assert.equal(await page.locator('.account-avatar img').count(),0);
    group.policy.active=false;user.group_id=0;user.group_name='';
    await page.goto(origin+'/index.html');await page.waitForFunction(()=>document.body.dataset.siteState==='ready');
    await page.locator('#fileInput').setInputFiles({name:'test.png',mimeType:'image/png',buffer:avatar});
    assert((await page.locator('#uploadMessage').textContent()).includes('有效用户组'));
    assert.equal(await page.locator('[data-upload-one]').count(),0);
    assert.equal(errors.length,0,errors.join('\n'));
    console.log('PASS group default + selection flag, all-group CRUD UI, guest assignment, personal quota inheritance, avatar upload/removal, no-group upload and 4 viewport widths');
  } finally {await browser.close();server.close();}
})().catch(err=>{console.error(err);process.exitCode=1;});
