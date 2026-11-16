(() => {
  // Cloud icon from the bundled Lucide distribution.
  window.AlbumIcons.Cloud = [["path", { d: "M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z" }]];
  const $ = s => document.querySelector(s);
  const escape = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const names = { local: "Local / NAS", s3: "S3 Compatible", r2: "Cloudflare R2", minio: "MinIO", b2: "Backblaze B2" };
  const bytes = n => n >= 1073741824 ? (n / 1073741824).toFixed(1) + " GB" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n + " B";
  let targets = [], defaultID = 0, busy = false, loading = false;
  const views = window.OrimgViews.setup("storageTargets", "storageViewTools", "orimg.storageView");
  let cacheLoaded = false, cacheLoading = false;
  function icon(name) {
    return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      (window.AlbumIcons[name] || window.AlbumIcons.Settings).map(([tag, attrs]) => '<' + tag + ' ' + Object.entries(attrs).map(([key, value]) => key + '="' + escape(value) + '"').join(" ") + '></' + tag + '>').join("") + '</svg>';
  }
  async function api(path = "", options = {}) {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 60000);
    try {
      const response = await fetch("/api/admin/storage/targets" + path, { ...options, signal: controller.signal, headers: { Authorization: "Bearer " + localStorage.getItem("zhuapic_token"), "Content-Type": "application/json" } });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "存储请求失败");
      return data;
    } catch (err) {
      if (err.name === "AbortError") throw new Error("请求超时，请刷新核对结果后再操作。");
      throw err;
    } finally { clearTimeout(timer); }
  }
  function notice(message, error = false) {
    const el = $("#targetNotice"); el.hidden = false; el.textContent = message; el.classList.toggle("is-error", error);
    if (typeof window.showSaveNotice === "function") window.showSaveNotice(message, error);
    const feedback = $("#targetEditorMessage"); if (feedback) { feedback.textContent = message; feedback.classList.toggle("is-error", error); }
  }
  async function reload() {
    if (loading || busy || !localStorage.getItem("zhuapic_token")) return;
    loading = true;
    try {
      const data = await api(); targets = data.targets || []; defaultID = data.default_target_id || 0;
      render(data.tasks || []);
      if (!cacheLoaded) loadCache();
      if (data.workers_disabled) notice("当前实例已暂停所有后台工作，队列不会执行。");
    } catch (err) { notice(err.message, true); } finally { loading = false; }
  }
  function render(tasks) {
    const status = $("#s3Status");
    if (status) { status.hidden = false; status.textContent = targets.filter(t => t.enabled && t.configured !== false).length + " 个存储可用"; }
    const sum = key => targets.reduce((n, t) => n + (t[key] || 0), 0);
    $("#targetSummary").textContent = targets.length + " 个位置 · " + sum("ready") + " 份副本 · " + bytes(sum("bytes"));
    $("#primaryTargetName").textContent = targets.find(t => t.id === defaultID)?.name || "Local + S3（原双副本策略）";
    $("#backupTargetNames").textContent = targets.filter(t => t.auto_backup && t.enabled).map(t => t.name).join("、") || "未启用";
    $("#storageTargets").innerHTML = targets.map(t => {
      const location = t.provider === "local" ? t.config.directory : [t.config.endpoint, t.config.bucket].filter(Boolean).join(" / ");
      return '<article class="storage-target" data-catalog-row data-search="' + escape(t.name + ' ' + location + ' ' + names[t.provider]) + '" data-filter="' + (t.enabled ? 'enabled' : 'paused') + (t.failed ? ' failed' : '') + '" data-target="' + t.id + '">' +
        '<div class="target-heading"><span class="target-symbol">' + icon(t.provider === "local" ? "HardDrive" : "Cloud") + '</span><div><h3>' + escape(t.name) + '</h3><small>' + names[t.provider] + '</small></div>' +
        '<button type="button" class="icon-button" data-edit="' + t.id + '" title="编辑存储" aria-label="编辑 ' + escape(t.name) + '">' + icon("Settings") + '</button></div>' +
        '<p class="target-location" title="' + escape(location) + '">' + escape(location || "尚未配置") + '</p>' +
        '<div class="target-counters"><span><b>' + t.ready + '</b> 份 · ' + bytes(t.bytes) + '</span><span>' + t.pending + ' 待同步</span><span class="' + (t.failed ? "is-error" : "") + '">' + t.failed + ' 失败</span></div>' +
        '<div class="target-roles"><label><input type="radio" name="default-upload-target" data-default="' + t.id + '"' + (t.id === defaultID ? ' checked' : '') + (!t.enabled || t.configured === false ? ' disabled' : '') + '>默认上传</label>' +
        '<label title="仅对允许自选存储的用户组开放；组内指定的默认位置不受此开关影响"><input type="checkbox" data-access="' + t.id + '"' + (t.upload_access === 'members' || t.upload_access === 'public' ? ' checked' : '') + '>用户可选</label>' +
        '<label><input type="checkbox" data-auto="' + t.id + '"' + (t.auto_backup ? ' checked' : '') + (!t.enabled || t.configured === false ? ' disabled' : '') + '>自动备份</label></div>' +
        '<div class="target-actions"><span class="target-state">' + (!t.enabled ? '已暂停' : t.configured === false ? '待配置' : t.id === defaultID ? '默认位置 · 按用户组策略' : t.upload_access === 'public' ? '游客与登录用户可选' : t.upload_access === 'members' ? '登录用户可选' : '管理员可选') + '</span>' +
        '<button type="button" class="ghost-button compact" data-action="backfill" data-id="' + t.id + '"' + (!t.enabled || t.configured === false ? ' disabled' : '') + '>补齐历史</button></div></article>';
    }).join("");
    views.refresh();
    const pending = targets.filter(t => t.pending || t.failed);
    $("#targetTasks").innerHTML = pending.length ? pending.map(t =>
      '<div class="target-task-row"><div><strong>' + escape(t.name) + '</strong><small>' + t.pending + ' 待同步 · ' + t.failed + ' 失败 · ' + t.ready + ' 已就绪</small></div><button class="ghost-button compact" data-action="retry" data-id="' + t.id + '"' + (!t.failed ? " disabled" : "") + '>重试失败项</button></div>').join("") +
      '<div class="target-task-details">' + tasks.map(task => '<div><strong>' + escape(task.filename) + '</strong><span>' + escape(task.target) + ' · ' + escape(({ pending: "等待同步", syncing: "复制并校验", verifying: "等待校验", verify_failed: "校验失败", failed: "失败" })[task.status] || task.status) + '</span>' + (task.error ? '<small class="is-error">' + escape(task.error) + '</small>' : "") + '</div>').join("") + '</div>' :
      '<p class="target-empty">没有待执行的多地副本任务</p>';
  }
  async function loadCache() {
    if (cacheLoading) return;
    cacheLoading=true; $("#refreshCache").disabled=true;
    const controller=new AbortController(), timer=setTimeout(() => controller.abort(),10000);
    try {
      const response=await fetch("/api/admin/storage/cache",{signal:controller.signal,headers:{Authorization:"Bearer " + localStorage.getItem("zhuapic_token")}});
      if (!response.ok) throw new Error("缓存统计暂时无法读取");
      const data=await response.json(), c=data.cache;
      if (!c) throw new Error("缓存统计暂时不可用");
      const unavailable=c.status === "unavailable", lower=c.partial ? "≥ " : "";
      $("#cacheBytes").textContent=unavailable ? "不可用" : lower+bytes(c.bytes);
      $("#cacheFiles").textContent=lower+c.files+" 个文件";
      $("#cacheExpired").textContent=unavailable ? "--" : lower+bytes(c.expired_bytes);
      $("#cacheExpiredFiles").textContent=lower+c.expired_files+" 个文件";
      $("#cacheTTL").textContent=Number((c.ttl_seconds/86400).toFixed(1))+" 天";
      $("#cacheCleanup").textContent=data.cleanup_enabled ? "每 " + Math.max(1,Math.round(data.cleanup_interval_seconds/60)) + " 分钟清理" : "自动清理已暂停";
      $("#cacheDirectory").textContent=c.directory;
      $("#cacheMessage").textContent=(unavailable ? "无法完整读取缓存目录，请检查目录权限。" : c.partial ? "本次扫描达到时间或数量上限，以上为已扫描部分的下限。" : c.status === "empty" ? "缓存目录尚未创建。" : "") + " 统计时间："+new Date(c.scanned_at).toLocaleString("zh-CN");
      $("#cacheMessage").classList.toggle("is-error",unavailable);
      $("#cacheFilesBody").innerHTML=(c.largest || []).map(file => '<tr><td><code>'+escape(file.name)+'</code></td><td>'+bytes(file.bytes)+'</td><td>'+escape(new Date(file.expires_at).toLocaleString("zh-CN"))+'</td></tr>').join("");
      $("#cacheDetails").hidden=!(c.largest || []).length;
      cacheLoaded=true;
    } catch (err) { $("#cacheMessage").textContent=err.name === "AbortError" ? "读取超时，请重试" : err.message; $("#cacheMessage").classList.add("is-error"); }
    finally { clearTimeout(timer); cacheLoading=false; $("#refreshCache").disabled=false; }
  }
  $("#refreshCache").innerHTML=icon("RefreshCw");
  $("#refreshCache").addEventListener("click",loadCache);
  async function change(question, action, success) {
    if (busy) return;
    if (!confirm(question)) return false;
    busy = true;
    notice("正在处理存储操作…");
    const controls = [...document.querySelectorAll(".storage-workspace button, .storage-workspace input, #targetEditor button, #targetEditor input, #targetEditor select, #targetTasks button")];
    const states = controls.map(el => el.disabled); controls.forEach(el => el.disabled = true);
    try { const result = await action(); notice(typeof result === "string" ? result : success); return true; }
    catch (err) { notice(err.message, true); return false; }
    finally { controls.forEach((el, i) => el.disabled = states[i]); busy = false; await reload(); }
  }
  async function action(id, actionName) {
    const t = targets.find(t => t.id === id); if (!t) return;
    const questions = {
      backfill: "为“" + t.name + "”补齐全部历史图片的副本？此操作会产生网络流量和存储占用，保留所有源文件。",
      default: "将“" + t.name + "”设为默认上传位置？只影响跟随站点策略的新上传，旧图片不移动。",
      retry: "重新尝试“" + t.name + "”的失败副本？",
      verify: "读取并校验“" + t.name + "”的副本？此操作会产生读取流量。",
      probe: "测试“" + t.name + "”的写入、读取和删除权限？将创建随机小文件并立即清理。"
    };
    await change(questions[actionName], async () => {
      const result = await api("/" + id + "/actions", { method: "POST", body: JSON.stringify({ action: actionName }) });
      if (actionName === "backfill") return result.queued ? "已加入 " + result.queued + " 个副本任务" : "没有缺失副本，无需补齐";
      if (actionName === "retry") return result.queued ? "已重新排队 " + result.queued + " 个失败项" : "没有可重试的失败项";
    }, actionName === "probe" ? "读写、校验与清理均通过" : actionName === "default" ? "默认上传位置已更新" : "任务已提交，可在后台任务页查看");
  }
  const dialog = document.createElement("dialog");
  dialog.id = "targetEditor";
  dialog.innerHTML = '<form id="targetEditorForm"><header><h2 id="targetEditorTitle">新增存储</h2><button type="button" class="icon-button" data-close aria-label="关闭">' + icon("X") + '</button></header>' +
    '<input type="hidden" name="id"><label>名称<input name="name" maxlength="80" required></label>' +
    '<label>存储服务<select name="provider">' + Object.entries(names).map(([key, label]) => '<option value="' + key + '">' + label + '</option>').join("") + '</select></label>' +
    '<div id="targetLocalFields"><label>目录<input name="directory" placeholder="/www/wwwroot/orimg-data"></label></div>' +
    '<div id="targetS3Fields"><label>Endpoint<input name="endpoint" type="url" placeholder="https://s3.example.com"></label>' +
    '<div class="target-form-pair"><label>Region<input name="region" placeholder="auto"></label><label>Bucket<input name="bucket"></label></div>' +
    '<label>对象前缀<input name="prefix" placeholder="images"></label>' +
    '<label>Access Key<input name="access_key" autocomplete="off"></label><label>Secret Key<input name="secret_key" type="password" autocomplete="new-password" placeholder="留空保留原密钥"></label>' +
    '<label class="target-check"><input name="path_style" type="checkbox" checked>Path-style</label></div>' +
    '<label class="target-check"><input name="enabled" type="checkbox" checked>启用存储</label>' +
    '<label>上传可选范围<select name="upload_access"><option value="admin">仅管理员</option><option value="members">登录用户</option><option value="public">游客与登录用户</option></select></label>' +
    '<p id="targetEditorMessage" role="status"></p>' +
    '<div class="target-editor-tools"><button type="button" class="ghost-button" data-probe>测试连接</button><button type="button" class="ghost-button" data-verify>校验副本</button><button type="button" class="danger-button" data-delete>删除存储</button></div>' +
    '<footer><button type="button" class="ghost-button" data-close>取消</button><button type="submit" class="primary-button">保存存储</button></footer></form>';
  document.body.append(dialog);
  const form = $("#targetEditorForm");
  function fields() { $("#targetLocalFields").hidden = form.elements.provider.value !== "local"; $("#targetS3Fields").hidden = form.elements.provider.value === "local"; }
  function edit(t) {
    form.reset(); form.elements.id.value = t?.id || ""; form.elements.name.value = t?.name || ""; form.elements.provider.value = t?.provider || "local";
    form.elements.enabled.checked = t ? t.enabled : true;
    form.elements.upload_access.value = t?.upload_access || "members";
    for (const key of ["directory", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key"]) form.elements[key].value = t?.config[key] || "";
    form.elements.path_style.checked = t ? t.config.path_style : true;
    form.elements.provider.disabled = Boolean(t);
    for (const key of ["directory", "endpoint", "bucket", "prefix", "path_style"]) form.elements[key].disabled = Boolean(t && t.location_locked !== false);
    for (const key of ["region", "access_key", "secret_key"]) form.elements[key].disabled = false;
    $("#targetEditorTitle").textContent = t ? t.name : "新增存储";
    $("#targetEditorMessage").textContent = t && t.location_locked !== false ? "位置已固定，原图路径不会被修改。密钥留空保留现有值。" : "";
    $("#targetEditorMessage").classList.remove("is-error");
    dialog.querySelector(".target-editor-tools").hidden = !t;
    dialog.querySelector("[data-delete]").disabled = Boolean(t?.legacy_mode);
    fields(); dialog.showModal(); form.elements.name.focus();
  }
  form.elements.provider.addEventListener("change", () => {
    fields(); const provider = form.elements.provider.value;
    form.elements.region.value = provider === "r2" ? "auto" : provider === "s3" || provider === "minio" ? "us-east-1" : "";
    form.elements.endpoint.placeholder = provider === "r2" ? "https://ACCOUNT_ID.r2.cloudflarestorage.com" : provider === "b2" ? "https://s3.us-west-004.backblazeb2.com" : "https://s3.example.com";
  });
  form.addEventListener("submit", async event => {
    event.preventDefault(); const id = Number(form.elements.id.value); const t = targets.find(t => t.id === id);
    const config = {}; for (const key of ["directory", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key"]) config[key] = form.elements[key].value.trim();
    config.path_style = form.elements.path_style.checked;
    const payload = { name: form.elements.name.value.trim(), provider: form.elements.provider.value, config, enabled: form.elements.enabled.checked, auto_backup: t?.auto_backup || false, upload_access: form.elements.upload_access.value };
    if (await change("确认保存存储“" + payload.name + "”？", () => api(id ? "/" + id : "", { method: id ? "PUT" : "POST", body: JSON.stringify(payload) }), "存储配置已保存")) dialog.close();
  });
  dialog.addEventListener("cancel", event => { if (busy) event.preventDefault(); });
  dialog.addEventListener("click", async event => {
    const button = event.target.closest("button"); if (!button || busy) return;
    const id = Number(form.elements.id.value);
    if (button.hasAttribute("data-close")) dialog.close();
    if (button.hasAttribute("data-probe")) await action(id, "probe");
    if (button.hasAttribute("data-verify")) await action(id, "verify");
    if (button.hasAttribute("data-delete") && await change("删除此存储配置？有图片引用的位置不能删除，远端文件不会被删除。", () => api("/" + id, { method: "DELETE" }), "存储配置已删除")) dialog.close();
  });
  $("#addStorageTarget").addEventListener("click", () => edit(null));
  document.addEventListener("click", event => {
    const editButton = event.target.closest("[data-edit]"); if (editButton?.closest("#storageTargets")) edit(targets.find(t => t.id === Number(editButton.dataset.edit)));
    const button = event.target.closest("[data-action][data-id]"); if (button?.closest("#storageTargets, #targetTasks")) action(Number(button.dataset.id), button.dataset.action);
  });
  $("#storageTargets").addEventListener("change", async event => {
    if (event.target.matches("[data-default]")) {
      const id = Number(event.target.dataset.default);
      document.querySelectorAll("[data-default]").forEach(input => input.checked = Number(input.dataset.default) === defaultID);
      await action(id, "default"); return;
    }
    if (event.target.matches("[data-access]")) {
      const t = targets.find(t => t.id === Number(event.target.dataset.access));
      const access = event.target.checked ? "members" : "admin";
      event.target.checked = t.upload_access === "members" || t.upload_access === "public";
      await change((access === "members" ? "向允许自选存储的登录用户组开放“" : "从普通用户的额外可选列表移除“") + t.name + "”？用户组指定的默认位置仍可使用。",
        () => api("/" + t.id, { method: "PUT", body: JSON.stringify({ ...t, upload_access: access }) }), "上传可选范围已更新"); return;
    }
    if (!event.target.matches("[data-auto]")) return;
    const t = targets.find(t => t.id === Number(event.target.dataset.auto)); const enabled = event.target.checked;
    event.target.checked = t.auto_backup;
    await change((enabled ? "为新上传启用“" : "停止新上传备份到“") + t.name + "”？已有副本保留，历史图片不会自动补齐。",
      () => api("/" + t.id, { method: "PUT", body: JSON.stringify({ ...t, auto_backup: enabled }) }), "备份策略已更新");
  });
  window.OrimgStorage = { reload, getTargets: () => targets };
})();
