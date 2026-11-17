(() => {
  // Cloud icon from the bundled Lucide distribution.
  window.AlbumIcons.Cloud = [["path", { d: "M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z" }]];
  const $ = s => document.querySelector(s);
  const escape = value => String(value ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
  const providers = { local: "Local / NAS", s3: "S3 Compatible", r2: "Cloudflare R2", minio: "MinIO", b2: "Backblaze B2", webdav: "WebDAV" };
  const bytes = n => n >= 1073741824 ? (n / 1073741824).toFixed(1) + " GB" : n >= 1048576 ? (n / 1048576).toFixed(1) + " MB" : n >= 1024 ? (n / 1024).toFixed(0) + " KB" : n + " B";
  const time = value => value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "";
  let storages = [], defaultID = 0, busy = false, loading = false;
  const views = window.OrimgViews.setup("storageTargets", "storageViewTools", "orimg.storageView");
  let cacheLoaded = false, cacheLoading = false;
  function icon(name) {
    return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      (window.AlbumIcons[name] || window.AlbumIcons.Settings).map(([tag, attrs]) => '<' + tag + ' ' + Object.entries(attrs).map(([key, value]) => key + '="' + escape(value) + '"').join(" ") + '></' + tag + '>').join("") + '</svg>';
  }
  async function api(path = "", options = {}, base = "/api/admin/storages") {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 60000);
    try {
      const response = await fetch(base + path, { ...options, signal: controller.signal, headers: { Authorization: "Bearer " + localStorage.getItem("zhuapic_token"), "Content-Type": "application/json" } });
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
    for (const id of ["#targetEditorMessage", "#removalMessage"]) { const feedback = $(id); if (feedback?.closest("dialog[open]")) { feedback.textContent = message; feedback.classList.toggle("is-error", error); } }
  }
  const location = s => s.provider === "local" ? s.config.directory : s.provider === "webdav" ? [s.config.endpoint, s.config.prefix].filter(Boolean).join(" / ") : [s.config.endpoint, s.config.bucket, s.config.prefix].filter(Boolean).join(" / ");
  function health(s) {
    if (s.status === "draining") return ["移除中", "is-warning"];
    if (s.status === "paused") return ["已暂停", ""];
    if (s.offline_until) return ["离线 · " + new Date(s.offline_until).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }) + " 重试", "is-error"];
    if (s.health === "ok") return ["在线", "is-ok"];
    return ["未检测", ""];
  }
  async function reload() {
    if (loading || busy || !localStorage.getItem("zhuapic_token")) return;
    loading = true;
    try {
      const data = await api(); storages = data.storages || []; defaultID = data.default_storage_id || 0;
      render(data.failures || [], data.stats || {});
      if (!cacheLoaded) loadCache();
      if (data.workers_disabled) notice("当前实例已暂停所有后台工作，副本同步与清理不会执行。");
      window.dispatchEvent(new CustomEvent("origin:storages", { detail: storages }));
    } catch (err) { notice(err.message, true); } finally { loading = false; }
  }
  function render(failures, stats) {
    const sum = key => storages.reduce((n, s) => n + (s[key] || 0), 0);
    $("#targetSummary").textContent = storages.length + " 个存储 · " + sum("ready") + " 份副本 · " + bytes(sum("bytes"));
    $("#primaryTargetName").textContent = storages.find(s => s.id === defaultID)?.name || "未设置";
    $("#backupTargetNames").textContent = storages.filter(s => s.auto_backup && s.status === "active").map(s => s.name).join("、") || "未启用";
    const metrics = { replicaPendingCount: stats.pending || 0, replicaFailureCount: (stats.failed || 0) + (stats.missing || 0), withoutCopyCount: stats.without_copy || 0, operationCount: stats.operations || 0 };
    for (const [id, value] of Object.entries(metrics)) { const el = document.getElementById(id); if (el) el.textContent = Number(value).toLocaleString("zh-CN"); }
    $("#storageTargets").innerHTML = storages.map(s => {
      const [state, tone] = health(s), where = location(s), writable = s.status === "active" && s.configured;
      const filter = [s.status, s.offline_until ? "offline" : "", s.failed || s.missing ? "failed" : ""].join(" ");
      return '<article class="storage-target" data-catalog-row data-search="' + escape(s.name + " " + where + " " + providers[s.provider]) + '" data-filter="' + filter + '" data-target="' + s.id + '">' +
        '<div class="target-heading"><span class="target-symbol">' + icon(s.provider === "local" ? "HardDrive" : "Cloud") + '</span><div><h3>' + escape(s.name) + '</h3><small>' + providers[s.provider] + ' · <span class="target-health ' + tone + '" title="' + escape(s.health_error || "") + '">' + state + '</span></small></div>' +
        '<button type="button" class="icon-button" data-edit="' + s.id + '" title="编辑存储" aria-label="编辑 ' + escape(s.name) + '">' + icon("Settings") + '</button></div>' +
        '<p class="target-location" title="' + escape(where) + '">' + escape(where || "尚未配置") + '</p>' +
        '<div class="target-counters"><span><b>' + s.ready + '</b> 份 · ' + bytes(s.bytes) + '</span><span>' + s.pending + ' 待同步</span><span class="' + (s.failed || s.missing ? "is-error" : "") + '">' + (s.failed + s.missing) + ' 异常</span></div>' +
        (s.status === "draining" ? '<p class="target-draining">正在迁出图片，完成后自动移除。进度见“后台任务”。</p>' :
        '<div class="target-roles"><label><input type="radio" name="default-upload-storage" data-default="' + s.id + '"' + (s.id === defaultID ? " checked" : "") + (!writable || !s.accept_uploads ? " disabled" : "") + '>默认上传</label>' +
        '<label title="允许作为上传位置；用户组可在此基础上开放给成员选择"><input type="checkbox" data-uploads="' + s.id + '"' + (s.accept_uploads ? " checked" : "") + (s.id === defaultID ? " disabled" : "") + '>可上传</label>' +
        '<label title="每张图片自动复制一份到这里"><input type="checkbox" data-auto="' + s.id + '"' + (s.auto_backup ? " checked" : "") + (!writable ? " disabled" : "") + '>自动备份</label></div>') +
        '<div class="target-actions"><span class="target-state' + (s.health_error && s.health !== "ok" ? ' is-error" title="' + escape(s.health_error) : "") + '">' + (s.health_error && s.health !== "ok" ? escape(s.health_error.length > 60 ? s.health_error.slice(0, 60) + "…" : s.health_error) : s.id === defaultID ? "默认上传位置" : s.auto_backup ? "备份位置" : s.accept_uploads ? "可作为上传位置" : "仅保存已有副本") + '</span>' +
        (s.status === "paused" ? '<button type="button" class="ghost-button compact" data-action="resume" data-id="' + s.id + '">恢复</button>' : "") +
        (s.offline_until ? '<button type="button" class="ghost-button compact" data-action="probe" data-id="' + s.id + '">重新检测</button>' : "") + '</div></article>';
    }).join("");
    views.refresh();
    const troubled = storages.filter(s => s.pending || s.failed || s.missing);
    $("#targetTasks").innerHTML = troubled.length ? troubled.map(s =>
      '<div class="target-task-row"><div><strong>' + escape(s.name) + '</strong><small>' + s.pending + ' 待同步 · ' + s.failed + ' 失败 · ' + s.missing + ' 缺失 · ' + s.ready + ' 已就绪</small></div><button class="ghost-button compact" data-action="retry" data-id="' + s.id + '"' + (!s.failed && !s.missing ? " disabled" : "") + '>重试异常项</button></div>').join("") +
      '<div class="target-task-details">' + failures.map(f => '<div><strong>' + escape(f.filename) + '</strong><span>' + escape(f.storage) + ' · ' + escape(({ pending: "等待重试", failed: "复制或校验失败", missing: "文件缺失，等待修复" })[f.state] || f.state) + ' · 第 ' + f.attempts + ' 次</span>' + (f.error ? '<small class="is-error">' + escape(f.error) + '</small>' : "") + '</div>').join("") + '</div>' :
      '<p class="target-empty">所有副本都已就绪</p>';
  }
  async function loadCache() {
    if (cacheLoading) return;
    cacheLoading = true; $("#refreshCache").disabled = true;
    const controller = new AbortController(), timer = setTimeout(() => controller.abort(), 10000);
    try {
      const response = await fetch("/api/admin/storage/cache", { signal: controller.signal, headers: { Authorization: "Bearer " + localStorage.getItem("zhuapic_token") } });
      if (!response.ok) throw new Error("缓存统计暂时无法读取");
      const data = await response.json(), c = data.cache;
      if (!c) throw new Error("缓存统计暂时不可用");
      const unavailable = c.status === "unavailable", lower = c.partial ? "≥ " : "";
      $("#cacheBytes").textContent = unavailable ? "不可用" : lower + bytes(c.bytes);
      $("#cacheFiles").textContent = lower + c.files + " 个文件";
      $("#cacheExpired").textContent = unavailable ? "--" : lower + bytes(c.expired_bytes);
      $("#cacheExpiredFiles").textContent = lower + c.expired_files + " 个文件";
      $("#cacheTTL").textContent = Number((c.ttl_seconds / 86400).toFixed(1)) + " 天";
      $("#cacheCleanup").textContent = data.cleanup_enabled ? "每 " + Math.max(1, Math.round(data.cleanup_interval_seconds / 60)) + " 分钟清理" : "自动清理已暂停";
      $("#cacheDirectory").textContent = c.directory;
      $("#cacheMessage").textContent = (unavailable ? "无法完整读取缓存目录，请检查目录权限。" : c.partial ? "本次扫描达到时间或数量上限，以上为已扫描部分的下限。" : c.status === "empty" ? "缓存目录尚未创建。" : "") + " 统计时间：" + time(c.scanned_at);
      $("#cacheMessage").classList.toggle("is-error", unavailable);
      $("#cacheFilesBody").innerHTML = (c.largest || []).map(file => '<tr><td><code>' + escape(file.name) + '</code></td><td>' + bytes(file.bytes) + '</td><td>' + escape(time(file.expires_at)) + '</td></tr>').join("");
      $("#cacheDetails").hidden = !(c.largest || []).length;
      cacheLoaded = true;
    } catch (err) { $("#cacheMessage").textContent = err.name === "AbortError" ? "读取超时，请重试" : err.message; $("#cacheMessage").classList.add("is-error"); }
    finally { clearTimeout(timer); cacheLoading = false; $("#refreshCache").disabled = false; }
  }
  $("#refreshCache").innerHTML = icon("RefreshCw");
  $("#refreshCache").addEventListener("click", loadCache);
  async function change(question, run, success) {
    if (busy) return false;
    if (question && !confirm(question)) return false;
    busy = true;
    notice("正在处理存储操作…");
    const controls = [...document.querySelectorAll(".storage-workspace button, .storage-workspace input, #targetEditor button, #targetEditor input, #targetEditor select, #storageRemoval button, #targetTasks button")];
    const states = controls.map(el => el.disabled); controls.forEach(el => el.disabled = true);
    try { const result = await run(); notice(typeof result === "string" ? result : success); return true; }
    catch (err) { notice(err.message, true); return false; }
    finally { controls.forEach((el, i) => el.disabled = states[i]); busy = false; await reload(); window.OrimgOperations?.reload(); }
  }
  async function action(id, name) {
    const s = storages.find(s => s.id === id); if (!s) return;
    const questions = {
      default: "将“" + s.name + "”设为默认上传位置？只影响跟随站点默认的新上传，已有图片不移动。",
      retry: "重新尝试“" + s.name + "”的失败与缺失副本？",
      verify: "读取并校验“" + s.name + "”上的全部副本？会产生读取流量；损坏或缺失的副本会自动从其他副本修复。",
      probe: "测试“" + s.name + "”的写入、读取和删除？将创建随机小文件并立即清理。",
      pause: "暂停“" + s.name + "”？暂停后不再写入新副本，已有图片仍可读取。",
      resume: "恢复“" + s.name + "”？",
    };
    await change(questions[name], async () => {
      const result = await api("/" + id + "/actions", { method: "POST", body: JSON.stringify({ action: name }) });
      if (name === "retry") return result.queued ? "已重新排队 " + result.queued + " 个副本" : "没有需要重试的副本";
      if (name === "verify") return "已开始校验，进度见“后台任务”";
    }, name === "probe" ? "读写、校验与清理均通过，存储在线" : name === "default" ? "默认上传位置已更新" : "已完成");
  }
  const save = s => api("/" + s.id, { method: "PUT", body: JSON.stringify(s) });

  // Editor
  const dialog = document.createElement("dialog");
  dialog.id = "targetEditor";
  dialog.innerHTML = '<form id="targetEditorForm"><header><h2 id="targetEditorTitle">新增存储</h2><button type="button" class="icon-button" data-close aria-label="关闭">' + icon("X") + '</button></header>' +
    '<input type="hidden" name="id"><label>名称<input name="name" maxlength="80" required></label>' +
    '<label>存储服务<select name="provider">' + Object.entries(providers).map(([key, label]) => '<option value="' + key + '">' + label + '</option>').join("") + '</select></label>' +
    '<div id="targetLocalFields"><label>目录<input name="directory" placeholder="/srv/origin/images 或 data/uploads"></label><p class="inline-help">绝对路径，或相对程序目录的路径（Docker 中为 /app）。</p></div>' +
    '<div id="targetS3Fields"><label><span data-s3-label="Endpoint" data-dav-label="WebDAV 地址">Endpoint</span><input name="endpoint" type="url" placeholder="https://s3.example.com"></label>' +
    '<div class="target-form-pair" data-s3-only><label>Region<input name="region" placeholder="auto"></label><label>Bucket<input name="bucket"></label></div>' +
    '<label><span data-s3-label="对象前缀" data-dav-label="子目录（可选）">对象前缀</span><input name="prefix" placeholder="images"></label>' +
    '<label><span data-s3-label="Access Key" data-dav-label="用户名">Access Key</span><input name="access_key" autocomplete="off"></label><label><span data-s3-label="Secret Key" data-dav-label="密码 / 应用专用密码">Secret Key</span><input name="secret_key" type="password" autocomplete="new-password" placeholder="留空保留原密钥"></label>' +
    '<p class="inline-help" data-dav-only>填写 WebDAV 目录地址，例如 Nextcloud 的 https://cloud.example.com/remote.php/dav/files/用户名/origin。目录会自动创建，建议使用 HTTPS。</p>' +
    '<label class="target-check" data-s3-only><input name="path_style" type="checkbox" checked>Path-style</label></div>' +
    '<div class="target-form-pair"><label class="target-check"><input name="active" type="checkbox" checked>启用</label><label class="target-check"><input name="accept_uploads" type="checkbox" checked>可作为上传位置</label></div>' +
    '<label class="target-check"><input name="auto_backup" type="checkbox">自动备份：每张图片都复制一份到这里（含已有图片）</label>' +
    '<label>读取优先级<input name="read_priority" type="number" min="1" max="1000" placeholder="本地 10 · 远程 100"><span class="inline-help">数字越小越先读取。</span></label>' +
    '<p id="targetEditorMessage" role="status"></p>' +
    '<div class="target-editor-tools"><button type="button" class="ghost-button" data-probe>测试连接</button><button type="button" class="ghost-button" data-verify>校验副本</button><button type="button" class="danger-button" data-remove>移除存储</button></div>' +
    '<footer><button type="button" class="ghost-button" data-close>取消</button><button type="submit" class="primary-button">保存存储</button></footer></form>';
  document.body.append(dialog);
  const form = $("#targetEditorForm");
  function fields() {
    const provider = form.elements.provider.value, dav = provider === "webdav";
    $("#targetLocalFields").hidden = provider !== "local"; $("#targetS3Fields").hidden = provider === "local";
    dialog.querySelectorAll("[data-s3-only]").forEach(el => { el.hidden = dav; });
    dialog.querySelectorAll("[data-dav-only]").forEach(el => { el.hidden = !dav; });
    dialog.querySelectorAll("[data-s3-label]").forEach(el => { el.textContent = dav ? el.dataset.davLabel : el.dataset.s3Label; });
  }
  function edit(s) {
    form.reset(); form.elements.id.value = s?.id || ""; form.elements.name.value = s?.name || ""; form.elements.provider.value = s?.provider || "local";
    for (const key of ["directory", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key"]) form.elements[key].value = s?.config[key] || "";
    form.elements.path_style.checked = s ? s.config.path_style : true;
    form.elements.active.checked = s ? s.status === "active" : true;
    form.elements.accept_uploads.checked = s ? s.accept_uploads : true;
    form.elements.auto_backup.checked = s ? s.auto_backup : false;
    form.elements.read_priority.value = s?.read_priority || "";
    form.elements.provider.disabled = Boolean(s);
    const locked = Boolean(s?.location_locked);
    for (const key of ["directory", "endpoint", "bucket", "prefix", "path_style"]) form.elements[key].disabled = locked;
    $("#targetEditorTitle").textContent = s ? s.name : "新增存储";
    $("#targetEditorMessage").textContent = locked ? "已有图片保存在这里，位置不能修改；密钥留空保留现有值。" : s?.health_error && s.health !== "ok" ? "最近错误：" + s.health_error : "";
    $("#targetEditorMessage").classList.toggle("is-error", Boolean(!locked && s?.health_error && s.health !== "ok"));
    dialog.querySelector(".target-editor-tools").hidden = !s;
    fields(); dialog.showModal(); form.elements.name.focus();
  }
  form.elements.provider.addEventListener("change", () => {
    fields(); const provider = form.elements.provider.value;
    form.elements.region.value = provider === "r2" ? "auto" : provider === "s3" || provider === "minio" ? "us-east-1" : "";
    form.elements.endpoint.placeholder = provider === "r2" ? "https://ACCOUNT_ID.r2.cloudflarestorage.com" : provider === "b2" ? "https://s3.us-west-004.backblazeb2.com" : provider === "webdav" ? "https://dav.example.com/origin" : "https://s3.example.com";
    form.elements.prefix.placeholder = provider === "webdav" ? "可留空" : "images";
  });
  form.addEventListener("submit", async event => {
    event.preventDefault(); const id = Number(form.elements.id.value);
    const config = {}; for (const key of ["directory", "endpoint", "region", "bucket", "prefix", "access_key", "secret_key"]) config[key] = form.elements[key].value.trim();
    config.path_style = form.elements.path_style.checked;
    const payload = { name: form.elements.name.value.trim(), provider: form.elements.provider.value, config, status: form.elements.active.checked ? "active" : "paused",
      accept_uploads: form.elements.accept_uploads.checked, auto_backup: form.elements.auto_backup.checked, read_priority: Number(form.elements.read_priority.value || 0) };
    if (await change("", () => api(id ? "/" + id : "", { method: id ? "PUT" : "POST", body: JSON.stringify(payload) }), "存储配置已保存" + (payload.auto_backup ? "，已有图片会在后台补齐备份" : ""))) dialog.close();
  });
  dialog.addEventListener("cancel", event => { if (busy) event.preventDefault(); });
  dialog.addEventListener("click", async event => {
    const button = event.target.closest("button"); if (!button || busy) return;
    const id = Number(form.elements.id.value);
    if (button.hasAttribute("data-close")) dialog.close();
    if (button.hasAttribute("data-probe")) await action(id, "probe");
    if (button.hasAttribute("data-verify")) await action(id, "verify");
    if (button.hasAttribute("data-remove")) { dialog.close(); openRemoval(id); }
  });

  // Removal
  const removal = document.createElement("dialog");
  removal.id = "storageRemoval";
  removal.className = "storage-removal";
  document.body.append(removal);
  async function openRemoval(id) {
    const s = storages.find(s => s.id === id); if (!s) return;
    removal.innerHTML = '<form><header><h2>移除“' + escape(s.name) + '”</h2><button type="button" class="icon-button" data-close aria-label="关闭">' + icon("X") + '</button></header><p id="removalMessage" role="status">正在分析…</p></form>';
    removal.showModal();
    let plan;
    try { plan = await api("/" + id + "/removal"); } catch (err) { $("#removalMessage").textContent = err.message; $("#removalMessage").classList.add("is-error"); return; }
    const affected = plan.only_here + plan.in_trash;
    const destinations = storages.filter(d => d.id !== id && d.status === "active" && d.configured);
    const blocked = plan.is_default ? "这是默认上传位置，请先在存储卡片上把默认位置切换到其他存储。" : "";
    const reach = plan.reachable ? "" : "<p class=\"is-error\">这个存储目前离线或连接失败，无法从它迁出图片。</p>";
    removal.querySelector("form").innerHTML = '<header><h2>移除“' + escape(s.name) + '”</h2><button type="button" class="icon-button" data-close aria-label="关闭">' + icon("X") + '</button></header>' +
      '<div class="removal-facts"><div><strong>' + plan.copies + '</strong><small>副本记录</small></div><div><strong class="' + (plan.only_here ? "is-error" : "") + '">' + plan.only_here + '</strong><small>只存在这里的图片</small></div><div><strong>' + plan.in_trash + '</strong><small>其中在回收站</small></div></div>' + reach +
      (plan.group_usage || plan.is_db_backup ? '<p class="inline-help">' + [plan.group_usage ? "用户组里对它的引用会被移除，相关用户改用默认位置" : "", plan.is_db_backup ? "数据库备份设置会被清空，需要重新选择备份位置" : ""].filter(Boolean).join("；") + '。</p>' : "") +
      '<fieldset class="removal-choice"><label><input type="radio" name="mode" value="drain"' + (plan.reachable || !affected ? " checked" : " disabled") + '><span><strong>' + (affected ? "迁出后移除（推荐）" : "移除") + '</strong><small>' +
      (affected ? "先把只存在这里的 " + affected + " 张图片复制到下面选择的存储并校验，全部完成后再移除。" : "所有图片在其他存储都有完整副本，可以直接移除。") + '</small></span></label>' +
      (affected ? '<label class="removal-indent">复制到<select name="destination">' + destinations.map(d => '<option value="' + d.id + '"' + (d.id === defaultID ? " selected" : "") + '>' + escape(d.name) + '</option>').join("") + '</select></label>' : "") +
      '<label class="target-check removal-indent"><input type="checkbox" name="delete_objects"' + (plan.reachable ? "" : " disabled") + '><span>同时删除这个存储上的图片文件' + (plan.reachable ? "（不勾选则文件保留，只移除配置）" : "（存储离线，无法删除；文件将保留）") + '</span></label>' +
      '<label><input type="radio" name="mode" value="force"' + (!plan.reachable && affected ? " checked" : "") + '><span><strong>强制移除</strong><small>立即移除，不访问这个存储。' + (plan.only_here ? "<b class=\"is-error\">只存在这里的 " + plan.only_here + " 张图片会移入回收站，之后无法访问。</b>" : "不影响任何图片。") + '</small></span></label></fieldset>' +
      '<p id="removalMessage" role="status" class="' + (blocked ? "is-error" : "") + '">' + escape(blocked) + '</p>' +
      '<footer><button type="button" class="ghost-button" data-close>取消</button><button type="submit" class="danger-button"' + (blocked ? " disabled" : "") + '>确认移除</button></footer>';
    removal.querySelector("form").onsubmit = async event => {
      event.preventDefault();
      const f = event.target, mode = f.elements.mode.value;
      if (!mode) return;
      const body = { mode, destination_id: Number(f.elements.destination?.value || 0), delete_objects: Boolean(f.elements.delete_objects?.checked) };
      const question = mode === "force" ? "强制移除“" + s.name + "”？" + (plan.only_here ? plan.only_here + " 张图片将移入回收站。" : "") + "此操作不可撤销。" : "开始移除“" + s.name + "”？" + (body.delete_objects ? "完成后会删除它上面的图片文件。" : "");
      if (await change(question, async () => {
        const result = await api("/" + id + "/removal", { method: "POST", body: JSON.stringify(body) });
        return mode === "force" ? "存储已移除" + (result.recycled_images ? "，" + result.recycled_images + " 张图片已移入回收站" : "") : affected ? "已开始迁出，进度见“后台任务”" : "存储已移除";
      })) removal.close();
    };
  }
  removal.addEventListener("click", event => { if (event.target.closest("[data-close]") && !busy) removal.close(); });
  removal.addEventListener("cancel", event => { if (busy) event.preventDefault(); });

  $("#addStorageTarget").addEventListener("click", () => edit(null));
  document.addEventListener("click", event => {
    const editButton = event.target.closest("[data-edit]"); if (editButton?.closest("#storageTargets")) edit(storages.find(s => s.id === Number(editButton.dataset.edit)));
    const button = event.target.closest("[data-action][data-id]"); if (button?.closest("#storageTargets, #targetTasks")) action(Number(button.dataset.id), button.dataset.action);
  });
  $("#storageTargets").addEventListener("change", async event => {
    const input = event.target, s = storages.find(s => s.id === Number(input.dataset.default || input.dataset.uploads || input.dataset.auto));
    if (!s) return;
    if (input.matches("[data-default]")) {
      document.querySelectorAll("[data-default]").forEach(el => el.checked = Number(el.dataset.default) === defaultID);
      await action(s.id, "default"); return;
    }
    if (input.matches("[data-uploads]")) {
      const enabled = input.checked; input.checked = s.accept_uploads;
      await change((enabled ? "允许把“" : "不再把“") + s.name + "”作为上传位置？" + (enabled ? "" : "已有图片不受影响，用户组里对它的选择会失效。"),
        () => save({ ...s, accept_uploads: enabled }), "上传设置已更新"); return;
    }
    if (input.matches("[data-auto]")) {
      const enabled = input.checked; input.checked = s.auto_backup;
      await change((enabled ? "把每张图片都备份到“" : "停止向“") + s.name + (enabled ? "”？已有图片也会在后台补齐。" : "”备份新图片？已有副本保留。"),
        () => save({ ...s, auto_backup: enabled }), "备份设置已更新");
    }
  });
  window.OrimgStorage = { reload, getTargets: () => storages };
})();
