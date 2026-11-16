const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

const state = {
  token: localStorage.getItem("zhuapic_token") || "",
  captchaToken: "",
  groups: [],
  users: [],
  userCursor: "",
  userFilters: { q: "", group: "", active: "" },
  adminImages: [],
  imageCursor: "",
  selectedImages: new Set(),
  imageFilters: { q: "", storage: "", from: "", to: "" },
  auditLogs: [],
  auditBefore: "",
};

const els = {
  loginShell: $("#loginShell"),
  loginForm: $("#loginForm"),
  loginMessage: $("#loginMessage"),
  captchaQuestion: $("#captchaQuestion"),
  refreshCaptcha: $("#refreshCaptcha"),
  adminLayout: $("#adminLayout"),
  adminMessage: $("#adminMessage"),
  panelTitle: $("#panelTitle"),
  panelSubtitle: $("#panelSubtitle"),
  s3Status: $("#s3Status"),
  adminBrandName: $("#adminBrandName"),
  adminBrandSubtitle: $("#adminBrandSubtitle"),
  adminBadge: $("#adminBadge"),
  logoPreview: $("#logoPreview"),
  logoFileInput: $("#logoFileInput"),
  chooseLogoButton: $("#chooseLogoButton"),
  logoUploadStatus: $("#logoUploadStatus"),
  dashboardLink: $("#dashboardLink"),
  siteForm: $("#siteForm"),
  securityForm: $("#securityForm"),
  storageForm: $("#storageForm"),
  reloadDashboard: $("#reloadDashboard"),
  metricImages: $("#metricImages"),
  metricImageBytes: $("#metricImageBytes"),
  metricToday: $("#metricToday"),
  metricTodayBytes: $("#metricTodayBytes"),
  metricUsers: $("#metricUsers"),
  metricUsersTotal: $("#metricUsersTotal"),
  metricReplicaErrors: $("#metricReplicaErrors"),
  metricReplicaPending: $("#metricReplicaPending"),
  metricDBConnections: $("#metricDBConnections"),
  metricDBWait: $("#metricDBWait"),
  metricJobs: $("#metricJobs"),
  metricJobsQueued: $("#metricJobsQueued"),
  metricVersion: $("#metricVersion"),
  activityChart: $("#activityChart"),
  localReplicaCount: $("#localReplicaCount"),
  localReplicaSize: $("#localReplicaSize"),
  s3ReplicaCount: $("#s3ReplicaCount"),
  s3ReplicaSize: $("#s3ReplicaSize"),
  replicaPendingCount: $("#replicaPendingCount"),
  replicaFailureCount: $("#replicaFailureCount"),
  diskUsageValue: $("#diskUsageValue"),
  diskUsageLimit: $("#diskUsageLimit"),
  reloadStorage: $("#reloadStorage"),
  storageJobs: $("#storageJobs"),
  runDatabaseBackup: $("#runDatabaseBackup"),
  groupsGrid: $("#groupsGrid"),
  reloadGroups: $("#reloadGroups"),
  showGroupCreator: $("#showGroupCreator"),
  cancelGroupCreator: $("#cancelGroupCreator"),
  groupCreateForm: $("#groupCreateForm"),
  groupPolicySource: $("#groupPolicySource"),
  usersTable: $("#usersTable"),
  createUserButton: $("#createUserButton"),
  userEditor: $("#userEditor"),
  userEditorForm: $("#userEditorForm"),
  userEditorTitle: $("#userEditorTitle"),
  userEditorSubtitle: $("#userEditorSubtitle"),
  userEditorClose: $("#userEditorClose"),
  userEditorCancel: $("#userEditorCancel"),
  userEditorBackdrop: $("#userEditorBackdrop"),
  userEditorDelete: $("#userEditorDelete"),
  userEditorGroup: $("#userEditorGroup"),
  userPasswordHelp: $("#userPasswordHelp"),
  reloadUsers: $("#reloadUsers"),
  userFilterForm: $("#userFilterForm"),
  userSearch: $("#userSearch"),
  userGroupFilter: $("#userGroupFilter"),
  userActiveFilter: $("#userActiveFilter"),
  resetUserFilter: $("#resetUserFilter"),
  loadMoreUsers: $("#loadMoreUsers"),
  adminImages: $("#adminImages"),
  imageScope: $("#imageScope"),
  reloadImages: $("#reloadImages"),
  loadMoreAdminImages: $("#loadMoreAdminImages"),
  adminImageFilterForm: $("#adminImageFilterForm"),
  adminImageSearch: $("#adminImageSearch"),
  adminImageStorage: $("#adminImageStorage"),
  adminImageFrom: $("#adminImageFrom"),
  adminImageTo: $("#adminImageTo"),
  resetAdminImageFilter: $("#resetAdminImageFilter"),
  adminImageSelectionBar: $("#adminImageSelectionBar"),
  adminSelectedImageCount: $("#adminSelectedImageCount"),
  copyAdminSelected: $("#copyAdminSelected"),
  deleteAdminSelected: $("#deleteAdminSelected"),
  clearAdminSelection: $("#clearAdminSelection"),
  auditActionFilter: $("#auditActionFilter"),
  reloadAudit: $("#reloadAudit"),
  auditList: $("#auditList"),
  loadMoreAudit: $("#loadMoreAudit"),
  reloadTrash: $("#reloadTrash"),
  trashImages: $("#trashImages"),
};

const panelCopy = {
  overview: ["系统概览", "上传、用户、容量和存储副本的实时状态。"],
  storage: ["存储", "存储位置、默认上传与多地副本。"],
  tasks: ["后台任务", "集中查看副本同步、完整性校验和数据库备份进度。"],
  mail: ["邮件", ""],
  site: ["页面设置", "品牌、公开页面内容、后台路径和站点域名。"],
  security: ["页面安全", "游客入口、验证码和接口限流。"],
  audit: ["审计日志", "追踪配置、账号、Token、图片和存储任务的关键操作。"],
  groups: ["用户组", "按组管理上传大小、频率、配额、存储与格式策略。"],
  users: ["用户管理", "创建用户、修改角色、启用禁用和重置密码。"],
  images: ["图库", ""],
  trash: ["回收站", "恢复误删图片，或立即清理所有存储副本。"],
};

let album;
let imageRequestVersion = 0;
let imagesLoading = false;
function getAlbum() {
  if (!album) album = new ZhuaAlbum({
    container: els.adminImages, filterForm: els.adminImageFilterForm, selectionBar: els.adminImageSelectionBar,
    selected: state.selectedImages, onSelection: renderAdminImageSelection, onDelete: deleteImage, writeClipboard,
  });
  return album;
}

async function request(path, options = {}) {
  const headers = options.headers || {};
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  const res = await fetch(path, { ...options, headers, signal: options.signal || AbortSignal.timeout(15000) });
  let data = {};
  try {
    data = await res.json();
  } catch {
    data = {};
  }
  if (!res.ok) { const error = new Error(data.error || `HTTP ${res.status}`); error.status = res.status; throw error; }
  return data;
}

function prettyBytes(bytes) {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(size >= 10 || unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function showMessage(el, text, ok = true) {
  el.textContent = text;
  el.style.color = ok ? "var(--green)" : "#c9342c";
}

async function loadCaptcha() {
  try {
    const data = await request("/api/auth/captcha");
    state.captchaToken = data.token;
    els.captchaQuestion.textContent = data.question || "...";
  } catch {
    els.captchaQuestion.textContent = "无需验证";
  }
}

async function login(event) {
  event.preventDefault();
  const form = new FormData(els.loginForm);
  try {
    const data = await request("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        email: form.get("email"),
        password: form.get("password"),
        captcha_token: state.captchaToken,
        captcha_answer: form.get("captcha_answer"),
      }),
    });
    state.token = data.token;
    localStorage.setItem("zhuapic_token", state.token);
    await bootstrapAdmin();
  } catch (err) {
    showMessage(els.loginMessage, err.message, false);
    await loadCaptcha();
  }
}

function logout() {
  state.token = "";
  localStorage.removeItem("zhuapic_token");
  $("#authStatus").hidden = true;
  els.loginShell.hidden = false;
  els.adminLayout.hidden = true;
  loadCaptcha();
}

async function enterAdmin() {
  const data = await request("/api/me");
  if (!data.user) throw new Error("账号信息暂时不可用");
  if (data.user.role !== "admin") {
    location.replace("/dashboard");
    return;
  }
  state.me = data.user;
  await loadSettings();
  window.OrimgAccount.mount($("#accountSlot"), { user: state.me, isAdminPage: true, userPath: els.dashboardLink.getAttribute("href") });
  $("#authStatus").hidden = true;
  els.loginShell.hidden = true;
  els.adminLayout.hidden = false;
  await loadGroups().catch(() => showMessage(els.loginMessage, "用户组暂时无法加载", false));
  await Promise.allSettled([loadDashboard(), loadUsers(), loadImages(true), loadStorageOverview(), loadTrash(), loadAudit(true)]);
  switchPanel(location.hash.replace("#", "") || "overview");
}

async function loadSettings() {
  const data = await request("/api/admin/settings");
  const settings = data.settings || {};
  const targets = await request("/api/admin/storage/targets");
  const backupTarget = $("#databaseBackupTarget");
  backupTarget.replaceChildren(new Option("原 S3 存储（兼容旧配置）", "0"));
  (targets.targets || []).forEach((target) => backupTarget.add(new Option(`${target.name}${target.enabled ? "" : "（已停用）"}`, String(target.id))));
  if (settings.db_backup_target_id && !Array.from(backupTarget.options).some((option) => option.value === settings.db_backup_target_id)) backupTarget.add(new Option("原备份存储不可用", settings.db_backup_target_id));
  fillForm(els.siteForm, settings);
  els.siteForm.elements.default_accent_color.value = settings.default_accent_color || "lime";
  els.siteForm.elements.background_overlay.value = settings.background_overlay || "82";
  els.siteForm.elements.background_blur.value = settings.background_blur || "0";
  window.ZhuaAppearance.setSiteDefault(settings.default_accent_color);
  fillForm(els.securityForm, settings);
  fillForm(els.storageForm, settings);
  backupTarget.value = settings.db_backup_target_id || "0";
  applyBrand(settings);
  window.OrimgBackground.syncEditor();
  if (settings.user_path) els.dashboardLink.href = settings.user_path;
}

function applyBrand(settings) {
  const brand = window.ZhuaBranding;
  brand.apply(settings);
  brand.setText(els.adminBrandName, brand.text(settings, "admin_brand_name", brand.text(settings, "site_name")));
  brand.setText(els.adminBrandSubtitle, brand.text(settings, "admin_brand_subtitle"));
  brand.setText(els.adminBadge, brand.text(settings, "admin_badge_text"));
}

function fillForm(form, settings) {
  Array.from(form.elements).forEach((field) => {
    if (!field.name) return;
    if (field.type === "checkbox") {
      field.checked = settings[field.name] === "true";
    } else if (field.type !== "radio") {
      field.value = settings[field.name] || "";
    }
  });
}

function formPayload(form) {
  const formData = new FormData(form);
  const payload = {};
  Array.from(form.elements).forEach((field) => {
    if (!field.name || field.type === "radio") return;
    if (field.type === "checkbox") {
      payload[field.name] = field.checked ? "true" : "false";
    } else {
      payload[field.name] = String(formData.get(field.name) || "");
    }
  });
  return payload;
}

const savingForms = new WeakSet();
let saveNoticeTimer;
function showSaveNotice(message, failed = false) {
  let notice = document.getElementById("saveFeedback");
  if (!notice) {
    notice = document.createElement("div");
    notice.id = "saveFeedback"; notice.className = "save-feedback";
    notice.setAttribute("role", "status"); notice.setAttribute("aria-live", "polite");
    document.body.append(notice);
  }
  clearTimeout(saveNoticeTimer);
  notice.hidden = false; notice.dataset.error = String(failed); notice.textContent = message;
  showMessage(els.adminMessage, message, !failed);
  saveNoticeTimer = setTimeout(() => { notice.hidden = true; }, failed ? 8000 : 4500);
}

async function confirmSave(form, question, action, successText) {
  if (savingForms.has(form) || !form.reportValidity()) return;
  savingForms.add(form);
  if (!confirm(question)) { savingForms.delete(form); return; }
  const controls = [...form.elements].map(el => ({ el, disabled: el.disabled }));
  const buttons = [...form.querySelectorAll('button[type="submit"]')].map(el => ({ el, text: el.textContent }));
  form.setAttribute("aria-busy", "true");
  controls.forEach(({ el }) => { el.disabled = true; });
  buttons.forEach(({ el }) => { el.textContent = "保存中…"; });
  showSaveNotice("正在保存，请稍候");
  try {
    await action();
    showSaveNotice(successText);
  } catch (err) {
    showSaveNotice(err.name === "TimeoutError" || err.name === "AbortError" ? "请求超时，保存结果尚未确认，请刷新核对后再重试。" : err.message, true);
    if (form.closest('.group-dialog')) $('#groupDialogStatus').textContent=err.message;
  } finally {
    controls.forEach(({ el, disabled }) => { el.disabled = disabled; });
    buttons.forEach(({ el, text }) => { el.textContent = text; });
    form.removeAttribute("aria-busy");
    savingForms.delete(form);
  }
}

async function saveSettings(form) {
  const payload = formPayload(form);
  const title = form === els.storageForm ? "运行设置" : form === els.securityForm ? "安全设置" : "页面设置";
  const detail = form === els.storageForm ? "将更新容量限制、缓存与后台服务设置。" : form === els.securityForm ? "将影响站点访问与验证规则。" : "将更新所有访客看到的页面外观与内容。";
  await confirmSave(form, `确认保存${title}？\n${detail}`, async () => {
    await request("/api/admin/settings", {
      method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload),
    });
    await loadSettings().catch(() => { throw new Error("设置已保存，但重新加载失败，请刷新页面核对。"); });
  }, `${title}已保存`);
}

async function uploadLogo(file) {
  if (!file) return;
  const form = new FormData();
  form.append("logo", file);
  try {
    els.logoUploadStatus.textContent = "上传中...";
    const data = await request("/api/admin/logo", {
      method: "POST",
      body: form,
    });
    els.siteForm.elements.site_logo_url.value = data.url;
    els.logoPreview.src = data.url;
    document.querySelectorAll(".brand-logo").forEach((img) => {
      img.src = data.url;
    });
    els.logoUploadStatus.textContent = "Logo 已上传并生效。";
  } catch (err) {
    els.logoUploadStatus.textContent = err.message;
  }
}

async function loadGroups() {
  const data = await request("/api/admin/groups");
  const storage = await request("/api/admin/storage/targets");
  state.storageTargets = storage.targets || [];
  state.groups = data.groups || [];
  state.guestGroupID = Number(data.guest_group_id || 0);
  const assignableOptions = state.groups
    .map((group) => `<option value="${group.id}">${escapeHTML(group.name)}</option>`)
    .join("");
  const sourceOptions = state.groups
    .map((group) => `<option value="${group.id}">${escapeHTML(group.name)}</option>`)
    .join("");
  const editorGroup = els.userEditorGroup.value;
  els.userEditorGroup.innerHTML = '<option value="0">无用户组 · 不可上传</option>' + assignableOptions;
  if (editorGroup) els.userEditorGroup.value = editorGroup;
  els.groupPolicySource.innerHTML = '<option value="0">新建空白策略</option>' + sourceOptions;
  $("#guestGroupSelect").innerHTML = '<option value="0">不分配 · 禁止游客上传</option>' + sourceOptions;
  $("#guestGroupSelect").value = String(state.guestGroupID);
  const selectedGroup = els.userGroupFilter.value;
  els.userGroupFilter.innerHTML = `<option value="">全部</option><option value="0">无用户组</option>${assignableOptions}`;
  els.userGroupFilter.value = selectedGroup;
  renderGroups();
}

async function loadDashboard() {
  const data = await request("/api/admin/dashboard");
  const metrics = data.metrics || {};
  els.metricImages.textContent = Number(metrics.images || 0).toLocaleString("zh-CN");
  els.metricImageBytes.textContent = prettyBytes(metrics.image_bytes || 0);
  els.metricToday.textContent = Number(metrics.uploads_today || 0).toLocaleString("zh-CN");
  els.metricTodayBytes.textContent = prettyBytes(metrics.upload_bytes_today || 0);
  els.metricUsers.textContent = Number(metrics.active_users || 0).toLocaleString("zh-CN");
  els.metricUsersTotal.textContent = `共 ${Number(metrics.users || 0).toLocaleString("zh-CN")} 位用户`;
  els.metricReplicaErrors.textContent = Number(metrics.failed_replicas || 0).toLocaleString("zh-CN");
  els.metricReplicaPending.textContent = `${Number(metrics.pending_replicas || 0).toLocaleString("zh-CN")} 个等待同步`;
  els.metricDBConnections.textContent = `${Number(metrics.db_in_use || 0)} / ${Number(metrics.db_open || 0)}`;
  els.metricDBWait.textContent = `${Number(metrics.db_idle || 0)} 空闲 · ${Number(metrics.db_wait_count || 0)} 次等待`;
  els.metricJobs.textContent = Number(metrics.running_jobs || 0).toLocaleString("zh-CN");
  els.metricJobsQueued.textContent = `${Number(metrics.queued_jobs || 0).toLocaleString("zh-CN")} 个排队或暂停`;
  els.metricVersion.textContent = data.app_version || "v0.3.0";
  renderActivity(data.activity || []);
}

function renderActivity(activity) {
  const byDate = new Map(activity.map((item) => [item.date, item]));
  const days = [];
  for (let offset = 6; offset >= 0; offset -= 1) {
    const date = new Date();
    date.setHours(12, 0, 0, 0);
    date.setDate(date.getDate() - offset);
    const key = `${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
    days.push(byDate.get(key) || { date: key, count: 0, bytes: 0 });
  }
  const max = Math.max(1, ...days.map((item) => Number(item.count || 0)));
  els.activityChart.innerHTML = days.map((item) => `
    <div class="activity-column" title="${item.count || 0} 张 · ${prettyBytes(item.bytes || 0)}">
      <div><i style="height:${Math.max(5, Math.round((Number(item.count || 0) / max) * 100))}%"></i></div>
      <strong>${Number(item.count || 0)}</strong>
      <span>${escapeHTML(item.date)}</span>
    </div>`).join("");
}

async function loadAudit(reset = true) {
  const params = new URLSearchParams({ limit: "50" });
  if (!reset && state.auditBefore) params.set("before", state.auditBefore);
  if (els.auditActionFilter?.value) params.set("action", els.auditActionFilter.value);
  const data = await request(`/api/admin/audit?${params.toString()}`);
  state.auditLogs = reset ? (data.logs || []) : state.auditLogs.concat(data.logs || []);
  state.auditBefore = String(data.next_before || "");
  els.loadMoreAudit.hidden = !data.has_more;
  renderAudit();
}

function renderAudit() {
  if (!state.auditLogs.length) {
    els.auditList.innerHTML = '<div class="storage-job-empty">暂无审计记录</div>';
    return;
  }
  const labels = {
    "auth.login": "登录成功",
    "settings.updated": "更新设置",
    "branding.logo_updated": "更新 Logo",
    "upload_policy.updated": "更新上传策略",
    "user_group.created": "创建用户组",
    "user_group.updated": "修改用户组",
    "user_group.deleted": "删除用户组",
    "user.created": "创建用户",
    "user.updated": "更新用户",
    "user.deleted": "删除用户",
    "profile.updated": "修改账号",
    "api_token.created": "创建 API Token",
    "api_token.revoked": "撤销 API Token",
    "image.deleted": "删除图片",
    "images.bulk_deleted": "批量删除图片",
    "image.restored": "恢复图片",
    "image.purged": "永久清理图片",
    "storage_job.created": "创建存储任务",
    "storage_job.controlled": "控制存储任务",
    "database_backup.created": "创建数据库备份",
  };
  els.auditList.innerHTML = state.auditLogs.map((item) => `
    <article class="audit-row">
      <div class="audit-mark"></div>
      <div class="audit-main"><strong>${escapeHTML(labels[item.action] || item.action)}</strong><span>${escapeHTML(item.actor_label || "system")} · ${escapeHTML(item.ip || "-")}</span></div>
      <div class="audit-target"><span>${escapeHTML(item.target_type || "-")}</span><code>${escapeHTML(item.target_id || "-")}</code></div>
      <time>${escapeHTML(new Date(item.created_at).toLocaleString("zh-CN"))}</time>
    </article>`).join("");
}

const groupViews = window.OrimgViews.setup("groupsGrid", "groupViewTools", "orimg.groupView", "list");
function groupStorageName(policy) {
  const target=(state.storageTargets || []).find(t => policy.storage_mode === "target:"+t.id || (t.legacy_mode && policy.storage_mode === t.legacy_mode));
  return target?.name || (policy.storage_mode === "mirror" ? "Local + S3" : "跟随站点");
}
function renderGroups() {
  els.groupsGrid.innerHTML = '<div class="group-columns" aria-hidden="true"><span>用户组</span><span>成员</span><span>单图 / 批量</span><span class="group-quota">总配额</span><span>上传位置</span><span>状态</span></div>' + state.groups.map((group) => {
    const policy = group.policy || {};
    const destination=groupStorageName(policy);
    return `<article class="group-entry" data-catalog-row data-group-id="${group.id}" data-search="${escapeAttr(group.name+' '+destination)}" data-filter="${policy.active ? 'enabled' : 'paused'}">
      <div class="group-summary" role="button" tabindex="0" aria-haspopup="dialog" aria-label="编辑用户组 ${escapeAttr(group.name)}"><div class="group-name"><strong>${escapeHTML(group.name)}</strong><small>${Number(group.id) === state.guestGroupID ? '游客使用此组' : policy.allow_storage_selection ? '可自选开放存储' : '仅默认存储'}</small></div>
      <div><small>成员</small><span>${group.member_count || 0}</span></div>
      <div><small>单图 / 批量</small><span>${policy.max_file_mb || 1} MB / ${policy.max_batch_count || 1} 张</span></div>
      <div class="group-quota"><small>总配额</small><span>${policy.total_quota_bytes ? prettyBytes(policy.total_quota_bytes) : '不限'}</span></div>
      <div class="group-destination"><small>上传位置</small><span title="${escapeAttr(destination)}">${escapeHTML(destination)}</span></div>
      <span class="group-active ${policy.active ? '' : 'is-error'}">${policy.active ? '已启用' : '已停用'}</span></div></article>`;
  }).join("");
  groupViews.refresh();
}
const groupDialog = document.createElement('dialog');
groupDialog.className = 'group-dialog';
groupDialog.setAttribute('aria-labelledby','groupDialogTitle');
groupDialog.innerHTML = '<header><div><h2 id="groupDialogTitle">编辑用户组</h2><p id="groupDialogStatus" role="status"></p></div><button type="button" class="ghost-button icon-button" aria-label="关闭用户组编辑" title="关闭">×</button></header><div class="group-editor"></div>';
document.body.append(groupDialog);
groupDialog.querySelector('header button').replaceChildren(window.OrimgGlyph('X'));
let groupDraft = '', groupTrigger;
function groupSnapshot() { const form=groupDialog.querySelector('form'); return form ? JSON.stringify([...new FormData(form)]) : ''; }
function closeGroupEditor() {
  if (groupDialog.querySelector('form')?.getAttribute('aria-busy') === 'true') return;
  if (groupSnapshot() !== groupDraft && !confirm('放弃尚未保存的用户组更改？')) return;
  groupDialog.close(); restoreGroupFocus();
}
function restoreGroupFocus() {
  const id=groupTrigger?.closest('.group-entry')?.dataset.groupId;
  if(id) els.groupsGrid.querySelector(`[data-group-id="${id}"] .group-summary`)?.focus();
}
function openGroupEditor(entry) {
  const group=state.groups.find(g => String(g.id)===entry.dataset.groupId); if(!group) return;
  groupTrigger=entry.querySelector('.group-summary');
  groupDialog.querySelector('.group-editor').innerHTML=groupPolicyForm(group);
  $('#groupDialogTitle').textContent=group.name;
  $('#groupDialogStatus').textContent='';
  groupDraft=groupSnapshot();groupDialog.showModal();
}
groupDialog.querySelector('header button').onclick=closeGroupEditor;
groupDialog.addEventListener('cancel',event=>{event.preventDefault();closeGroupEditor();});
groupDialog.addEventListener('submit',event=>{event.preventDefault();savePolicy(event.target);});
groupDialog.addEventListener('click',event=>{const button=event.target.closest('[data-delete-group]');if(button) deleteGroup(button.dataset.deleteGroup);});
function groupPolicyForm(group) {
    const policy=group.policy || {};
    return `
      <form class="policy-card" data-policy-id="${policy.id}" data-group-id="${group.id}">
        <div class="policy-content">
        <div class="policy-head">
          <label class="policy-name-field"><span>用户组名称</span><input name="group_name" maxlength="80" value="${escapeAttr(group.name)}"></label>
          <div class="policy-head-meta">
            <small>${group.member_count || 0} 位用户</small>
          </div>
        </div>
        <div class="policy-fields">
          <label>单图上限 MB<input name="max_file_mb" type="number" min="1" max="200" value="${policy.max_file_mb || 1}"></label>
          <label>单批张数<input name="max_batch_count" type="number" min="1" max="100" value="${policy.max_batch_count || 1}"></label>
          <label>每日张数<input name="max_daily_uploads" type="number" min="0" value="${policy.max_daily_uploads || 0}"></label>
          <label>每日流量 GB<input name="max_daily_gb" type="number" min="0" step="0.1" value="${bytesToGB(policy.max_daily_bytes)}"></label>
          <label>总配额 GB<input name="total_quota_gb" type="number" min="0" step="0.1" value="${bytesToGB(policy.total_quota_bytes)}"></label>
          <label>图片保留天数<input name="retention_days" type="number" min="0" max="3650" value="${policy.retention_days || 0}"><span class="inline-help">0 表示永久</span></label>
          <label>默认上传位置<select name="storage_mode">
            <option value="default"${policy.storage_mode === "default" ? " selected" : ""}>跟随站点默认</option>
            ${(state.storageTargets || []).map(target => {
              const selected = policy.storage_mode === "target:" + target.id || (target.legacy_mode && policy.storage_mode === target.legacy_mode);
              return `<option value="target:${target.id}"${selected ? " selected" : ""}${!target.enabled || target.configured === false ? " disabled" : ""}>${escapeHTML(target.name)}</option>`;
            }).join("")}
            ${policy.storage_mode === "mirror" ? '<option value="mirror" selected>Local + S3（原双副本策略）</option>' : ""}
          </select></label>
        </div>
        <label>允许格式<input name="allowed_types" value="${escapeAttr(policy.allowed_types || "")}"></label>
        <div class="policy-switches">
          <label class="switch-line"><span>允许自选开放存储</span><input name="allow_storage_selection" type="checkbox"${policy.allow_storage_selection ? " checked" : ""}></label>
          <label class="switch-line"><span>允许 SVG</span><input name="allow_svg" type="checkbox"${policy.allow_svg ? " checked" : ""}></label>
          <label class="switch-line"><span>允许 API Token</span><input name="allow_api" type="checkbox"${policy.allow_api ? " checked" : ""}></label>
          <label class="switch-line"><span>策略启用</span><input name="active" type="checkbox"${policy.active ? " checked" : ""}></label>
        </div>
        </div>
        <div class="policy-actions">
          <button class="danger-text-button" data-delete-group="${group.id}" type="button">删除用户组</button>
          <button class="primary-button compact" type="submit">保存更改</button>
        </div>
      </form>`;
}

function bytesToGB(bytes) {
  if (!Number(bytes)) return 0;
  return Math.round((Number(bytes) / 1073741824) * 10) / 10;
}

function gbToBytes(value) {
  return Math.max(0, Math.round(Number(value || 0) * 1073741824));
}

async function savePolicy(form) {
  const fields = new FormData(form);
  const payload = {
    max_file_mb: Number(fields.get("max_file_mb")),
    max_batch_count: Number(fields.get("max_batch_count")),
    max_daily_uploads: Number(fields.get("max_daily_uploads")),
    max_daily_bytes: gbToBytes(fields.get("max_daily_gb")),
    total_quota_bytes: gbToBytes(fields.get("total_quota_gb")),
    storage_mode: String(fields.get("storage_mode") || "default"),
    allow_storage_selection: form.elements.allow_storage_selection.checked,
    retention_days: Number(fields.get("retention_days") || 0),
    allowed_types: String(fields.get("allowed_types") || ""),
    allow_svg: form.elements.allow_svg.checked,
    allow_api: form.elements.allow_api.checked,
    active: form.elements.active.checked,
  };
  await confirmSave(form, `确认保存用户组“${fields.get("group_name") || ""}”及上传策略？更改将应用于该组所有用户。`, async () => {
    await request(`/api/admin/groups/${form.dataset.groupId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: String(fields.get("group_name") || "") }),
    });
    await request(`/api/admin/policies/${form.dataset.policyId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    await loadGroups().catch(() => { throw new Error("用户组已保存，但重新加载失败，请刷新核对。"); });
    groupDraft=groupSnapshot(); groupDialog.close(); restoreGroupFocus();
  }, "用户组与上传策略已保存");
}

function toggleGroupCreator(show) {
  els.groupCreateForm.hidden = !show;
  if (show) {
    els.groupCreateForm.elements.name.focus();
  } else {
    els.groupCreateForm.reset();
  }
}

async function createGroup(event) {
  event.preventDefault();
  const fields = new FormData(els.groupCreateForm);
  try {
    await request("/api/admin/groups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        name: String(fields.get("name") || ""),
        copy_from_group_id: Number(fields.get("copy_from_group_id") || 0),
      }),
    });
    toggleGroupCreator(false);
    showMessage(els.adminMessage, "用户组已创建，可继续调整上传策略");
    await loadGroups();
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

async function deleteGroup(id) {
  const group = state.groups.find((item) => Number(item.id) === Number(id));
  if (!group || !confirm(`删除用户组“${group.name}”？${group.member_count || 0} 位成员将变为未分组，无法继续上传。已有账号、图片和管理员身份保留。${Number(id) === state.guestGroupID ? '游客上传也将关闭。' : ''}`)) return;
  try {
    await request(`/api/admin/groups/${id}`, { method: "DELETE" });
    showMessage(els.adminMessage, "用户组已删除");
    await Promise.all([loadGroups(), loadUsers(true)]);
    groupDialog.close();
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

async function loadUsers(reset = true) {
  const params = new URLSearchParams({ limit: "50" });
  if (!reset && state.userCursor) params.set("before", state.userCursor);
  for (const [key, value] of Object.entries(state.userFilters)) if (value) params.set(key, value);
  const data = await request(`/api/admin/users?${params.toString()}`);
  if (reset) state.users = [];
  state.users.push(...(data.users || []));
  state.userCursor = data.next_before ? String(data.next_before) : "";
  els.loadMoreUsers.hidden = !data.has_more;
  renderUsers();
}

function renderUsers() {
  if (!state.users.length) {
    els.usersTable.innerHTML = '<div class="storage-job-empty">没有符合条件的用户</div>';
    return;
  }
  els.usersTable.innerHTML = state.users.map((user) => {
    const initial = Array.from(user.username || user.email || "U")[0] || "U";
    return `
      <article class="user-list-row${user.active ? "" : " is-disabled"}" data-user="${user.id}">
        <div class="user-avatar" aria-hidden="true">${/^\/avatars\/[a-f0-9]{32}\.png$/.test(user.avatar_url || '') ? `<img src="${escapeAttr(user.avatar_url)}" alt="" loading="lazy">` : escapeHTML(initial.toUpperCase())}</div>
        <div class="user-identity">
          <strong>${escapeHTML(user.username)}</strong>
          <span>${escapeHTML(user.email || "未设置邮箱")}</span>
        </div>
        <div class="user-list-meta"><span>角色</span><strong>${user.role === "admin" ? "管理员" : "普通用户"}</strong></div>
        <div class="user-list-meta"><span>用户组</span><strong>${escapeHTML(user.group_name || "未分组")}</strong></div>
        <div class="user-list-meta"><span>图片 / 容量</span><strong>${Number(user.image_count || 0).toLocaleString("zh-CN")} 张</strong><small>${user.quota_bytes == null ? '跟随用户组' : user.quota_bytes === 0 ? '不限容量' : prettyBytes(user.quota_bytes)}</small></div>
        <span class="user-status ${user.active ? "is-active" : "is-inactive"}">${user.active ? "已启用" : "已停用"}</span>
        <button class="ghost-button compact" data-edit-user="${user.id}" type="button">编辑</button>
      </article>`;
  }).join("");
}

function applyUserFilters(event) {
  event.preventDefault();
  state.userFilters = {
    q: els.userSearch.value.trim(),
    group: els.userGroupFilter.value,
    active: els.userActiveFilter.value,
  };
  loadUsers(true).catch((err) => showMessage(els.adminMessage, err.message, false));
}

function resetUserFilters() {
  els.userFilterForm.reset();
  state.userFilters = { q: "", group: "", active: "" };
  loadUsers(true).catch((err) => showMessage(els.adminMessage, err.message, false));
}

function groupOptions(selectedID) {
  return `<option value="0"${!Number(selectedID) ? ' selected' : ''}>无用户组 · 不可上传</option>` + state.groups
    .map((group) => `<option value="${group.id}"${Number(selectedID) === Number(group.id) ? " selected" : ""}>${escapeHTML(group.name)}</option>`)
    .join("");
}

function openUserEditor(user = null) {
  const form = els.userEditorForm;
  form.reset();
  form.elements.id.value = user?.id || "";
  form.elements.username.value = user?.username || "";
  form.elements.email.value = user?.email || "";
  form.elements.role.value = user?.role || "user";
  form.elements.group_id.innerHTML = groupOptions(user?.group_id || 0);
  form.elements.quota_mode.value = user?.quota_bytes == null ? "inherit" : user.quota_bytes === 0 ? "unlimited" : "custom";
  form.elements.quota_gb.value = user?.quota_bytes > 0 ? Number((user.quota_bytes / 1073741824).toFixed(9)) : "5";
  form.elements.quota_gb.disabled = form.elements.quota_mode.value !== "custom";
  form.elements.quota_mode.onchange = () => { form.elements.quota_gb.disabled = form.elements.quota_mode.value !== "custom"; };
  form.elements.active.checked = user ? Boolean(user.active) : true;
  els.userEditorTitle.textContent = user ? user.username : "新增用户";
  els.userEditorSubtitle.textContent = user ? "修改登录资料、角色、用户组或重置密码。" : "创建登录账号并分配用户组。";
  els.userPasswordHelp.textContent = user ? "留空则保持当前密码" : "新用户密码至少 6 位";
  els.userEditorDelete.hidden = !user;
  els.userEditor.hidden = false;
  document.body.classList.add("modal-open");
  window.setTimeout(() => form.elements.username.focus(), 0);
}

function closeUserEditor() {
  els.userEditor.hidden = true;
  document.body.classList.remove("modal-open");
}

async function saveUser(event) {
  event.preventDefault();
  const form = els.userEditorForm;
  const fields = new FormData(form);
  const id = String(fields.get("id") || "");
  const payload = {
    username: fields.get("username"),
    email: fields.get("email"),
    role: fields.get("role"),
    group_id: Number(fields.get("group_id")),
    active: form.elements.active.checked,
    password: fields.get("password"),
    quota_bytes: fields.get("quota_mode") === "inherit" ? null : fields.get("quota_mode") === "unlimited" ? 0 : gbToBytes(fields.get("quota_gb")),
  };
  await confirmSave(form, `确认${id ? "保存" : "创建"}用户“${payload.username || payload.email}”？账号权限及密码的修改将立即生效。`, async () => {
    await request(id ? `/api/admin/users/${id}` : "/api/admin/users", {
      method: id ? "PUT" : "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    closeUserEditor();
    await Promise.all([loadUsers(true), loadDashboard(), loadGroups()]).catch(() => { throw new Error("用户已保存，但重新加载失败，请刷新核对。"); });
  }, id ? "用户已保存" : "用户已创建");
}

async function deleteUser(id) {
  if (!confirm("删除这个用户？用户图片会变为游客图片。")) return;
  try {
    await request(`/api/admin/users/${id}`, { method: "DELETE" });
    closeUserEditor();
    showMessage(els.adminMessage, "用户已删除");
    await Promise.all([loadUsers(true), loadDashboard(), loadGroups()]);
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

async function loadImages(reset = true) {
  if (!reset && imagesLoading) return;
  const version = ++imageRequestVersion;
  imagesLoading = true;
  els.loadMoreAdminImages.disabled = true;
  els.reloadImages.disabled = true;
  els.adminImages.setAttribute("aria-busy", "true");
  try {
    const params = new URLSearchParams({ limit: "60", scope: els.imageScope?.value || "all" });
    if (!reset && state.imageCursor) params.set("cursor", state.imageCursor);
    for (const [key, value] of Object.entries(state.imageFilters)) if (value) params.set(key, value);
    const data = await request(`/api/admin/images?${params.toString()}`);
    if (version !== imageRequestVersion) return;
    state.adminImages = reset ? (data.images || []) : Array.from(new Map([...state.adminImages, ...(data.images || [])].map((image) => [image.id, image])).values());
    if (reset) state.selectedImages.clear();
    state.imageCursor = data.next_cursor || "";
    els.loadMoreAdminImages.hidden = !data.has_more;
    renderAlbum(els.adminImages, state.adminImages);
    renderAdminImageSelection();
  } finally {
    if (version === imageRequestVersion) {
      imagesLoading = false;
      els.loadMoreAdminImages.disabled = false;
      els.reloadImages.disabled = false;
      els.adminImages.setAttribute("aria-busy", "false");
    }
  }
}
async function loadStorageOverview() {
	window.OrimgStorage?.reload();
  if (!state.token) return;
  try {
    const data = await request("/api/admin/storage");
    const stats = data.stats || {};
    els.localReplicaCount.textContent = `${stats.local_ready || 0} 份`;
    els.localReplicaSize.textContent = prettyBytes(stats.local_bytes || 0);
    els.s3ReplicaCount.textContent = `${stats.s3_ready || 0} 份`;
    els.s3ReplicaSize.textContent = prettyBytes(stats.s3_bytes || 0);
    els.replicaPendingCount.textContent = String(stats.pending || 0);
    els.replicaFailureCount.textContent = `${stats.failed || 0} 个异常`;
    const usage = Number(stats.disk_used_percent || 0);
    els.diskUsageValue.textContent = stats.disk_used_percent == null ? "未知" : `${usage.toFixed(1)}%`;
    els.diskUsageLimit.textContent = `安全线 ${stats.disk_stop_percent || 85}%`;
    els.diskUsageValue.classList.toggle("danger-text", usage >= Number(stats.disk_stop_percent || 85));
    renderStorageJobs(data.jobs || []);
  } catch (err) {
    if (!els.adminLayout.hidden) showMessage(els.adminMessage, err.message, false);
  }
}

function renderStorageJobs(jobs) {
  if (!jobs.length) {
    els.storageJobs.innerHTML = '<div class="storage-job-empty">暂无副本同步、校验或备份任务</div>';
    return;
  }
  els.storageJobs.innerHTML = jobs
    .map((job) => {
      if (job.type === "db_backup" && job.status === "completed") job = { ...job, total_items: 1, processed_items: 1, success_items: 1, failed_items: 0 };
      const total = Number(job.total_items || 0);
      const done = Number(job.success_items || 0) + Number(job.failed_items || 0);
      const percent = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 100;
      const isLocalTarget = job.target_mode === "local";
      const title = job.type === "verify" ? "校验现有副本" : job.type === "db_backup" ? "数据库逻辑备份" : isLocalTarget ? "补齐本地副本" : "补齐云端副本";
      const detail = job.type === "verify"
        ? "读取现有文件并比对完整性，不写入也不删除文件。"
        : job.type === "db_backup"
          ? `MySQL 完整逻辑备份 · ${$("#databaseBackupTarget").querySelector(`option[value="${String(job.target_mode).replace("target:", "")}"]`)?.textContent || (job.target_mode === "s3" ? "原 S3 存储" : job.target_mode)}`
          : `${job.source_mode === "s3" ? "S3" : "Local"} → ${isLocalTarget ? "Local" : "S3"}，仅复制缺失副本，源文件保留。`;
      const status = storageJobStatus(job.status);
      const completedWithoutWork = job.status === "completed" && total === 0;
      const progressCopy = completedWithoutWork
        ? "目标端已完整，无需复制"
        : `${Number(job.success_items || 0).toLocaleString("zh-CN")} 成功 · ${Number(job.failed_items || 0).toLocaleString("zh-CN")} 失败 · ${Math.max(0, total - done).toLocaleString("zh-CN")} 待处理`;
      let action = "";
      if (job.type !== "db_backup" && (job.status === "running" || job.status === "queued")) action = `<button data-job-action="pause" data-job-id="${job.id}" type="button">暂停</button>`;
      if (job.status === "paused") action = `<button data-job-action="resume" data-job-id="${job.id}" type="button">继续</button>`;
      if (job.status === "completed_with_errors") action = `<button data-job-action="retry" data-job-id="${job.id}" type="button">${job.type === "db_backup" ? "重新备份" : "重试失败项"}</button>`;
      return `
        <article class="storage-job-row">
          <div class="storage-job-main">
            <div class="storage-job-title"><strong>${escapeHTML(title)}</strong><span class="job-status status-${escapeAttr(job.status)}">${status}</span></div>
            <p>${escapeHTML(detail)}</p>
            <div class="storage-job-track"><i style="width:${percent}%"></i></div>
            <div class="storage-job-foot"><small>${escapeHTML(progressCopy)} · ${prettyBytes(job.processed_bytes || 0)} / ${prettyBytes(job.total_bytes || 0)}${job.last_error ? ` · ${escapeHTML(job.last_error)}` : ""}</small><time>${job.created_at ? escapeHTML(new Date(job.created_at).toLocaleString("zh-CN")) : ""}</time></div>
          </div>
          <div class="storage-job-action">${action}</div>
        </article>`;
    })
    .join("");
}

async function runDatabaseBackup() {
  if (els.runDatabaseBackup.disabled || !confirm("按已保存的备份设置创建 MySQL 备份？备份包含账号和加密配置，请确保目标存储私有。")) return;
  els.runDatabaseBackup.disabled = true;
  try {
    await request("/api/admin/storage/backup", { method: "POST" });
    showMessage(els.adminMessage, "数据库备份已加入后台队列");
    await loadStorageOverview();
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  } finally {
    els.runDatabaseBackup.disabled = false;
  }
}

function storageJobStatus(status) {
  return ({
    queued: "等待中",
    running: "进行中",
    paused: "已暂停",
    completed: "已完成",
    completed_with_errors: "部分失败",
  })[status] || status;
}

async function controlStorageJob(id, action) {
  try {
    await request(`/api/admin/storage/jobs/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action }),
    });
    await loadStorageOverview();
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

async function loadTrash() {
  if (!state.token) return;
  try {
    const data = await request("/api/admin/trash");
    renderTrash(data.images || []);
  } catch (err) {
    if (!els.adminLayout.hidden) showMessage(els.adminMessage, err.message, false);
  }
}

function renderTrash(images) {
  if (!images.length) {
    els.trashImages.innerHTML = '<div class="storage-job-empty trash-empty">回收站是空的</div>';
    return;
  }
  els.trashImages.innerHTML = images.map((image) => {
    const deadline = image.delete_after ? new Date(image.delete_after).toLocaleString("zh-CN") : "等待清理";
    return `
      <article class="trash-row">
        <div class="trash-icon"><span></span></div>
        <div class="trash-copy">
          <strong title="${escapeAttr(image.original_name)}">${escapeHTML(image.original_name)}</strong>
          <small>${prettyBytes(image.size)} · ${escapeHTML(image.storage_mode)} · ${escapeHTML(deadline)} 后清理</small>
        </div>
        <button class="ghost-button compact" data-restore-image="${image.id}" type="button">恢复</button>
        <button class="danger-button compact" data-purge-image="${image.id}" type="button">永久删除</button>
      </article>`;
  }).join("");
}

async function restoreImage(id) {
  try {
    await request(`/api/admin/trash/${id}/restore`, { method: "POST" });
    showMessage(els.adminMessage, "图片已恢复");
    await Promise.all([loadTrash(), loadImages(), loadStorageOverview(), loadDashboard()]);
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

async function purgeImage(id) {
  if (!confirm("永久删除这张图片的全部 Local 与 S3 副本？此操作无法撤销。")) return;
  try {
    await request(`/api/admin/trash/${id}`, { method: "DELETE" });
    showMessage(els.adminMessage, "图片已进入永久清理流程");
    await Promise.all([loadTrash(), loadStorageOverview(), loadDashboard()]);
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

function renderAlbum(container, images) {
  getAlbum().setImages(images);
}

function renderAdminImageSelection() {
  const count = state.selectedImages.size;
  els.adminImageSelectionBar.hidden = count === 0;
  els.adminSelectedImageCount.textContent = String(count);
  album?.syncSelection();
}

function applyAdminImageFilters(event) {
  event.preventDefault();
  state.imageFilters = {
    q: els.adminImageSearch.value.trim(),
    storage: els.adminImageStorage.value,
    from: els.adminImageFrom.value,
    to: els.adminImageTo.value,
  };
  loadImages(true).catch((err) => showMessage(els.adminMessage, err.message, false));
}

function resetAdminImageFilters() {
  els.adminImageFilterForm.reset();
  state.imageFilters = { q: "", storage: "", from: "", to: "" };
  loadImages(true).catch((err) => showMessage(els.adminMessage, err.message, false));
}

async function copyAdminSelectedImages() {
  await getAlbum().copySelected();
}

async function deleteAdminSelectedImages() {
  const ids = Array.from(state.selectedImages);
  if (ids.length > 100) { getAlbum().announce("每次最多删除 100 张图片，请减少选择", true); return; }
  if (!ids.length || !confirm(`将选中的 ${ids.length} 张图片移至回收站？`)) return;
  try {
    const data = await request("/api/admin/images/bulk", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "delete", ids }),
    });
    showMessage(els.adminMessage, `${data.deleted || 0} 张图片已移至回收站`);
    await Promise.all([loadImages(true), loadTrash(), loadDashboard(), loadAudit(true)]);
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

async function writeClipboard(value) {
  const text = String(value || "");
  if (!text) throw new Error("empty clipboard value");
  if (legacyClipboardCopy(text)) return;
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  throw new Error("clipboard unavailable");
}

function legacyClipboardCopy(text) {
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.readOnly = true;
  textarea.style.position = "fixed";
  textarea.style.inset = "-9999px auto auto -9999px";
  const focused = document.activeElement;
  (document.querySelector("dialog[open]") || document.body).appendChild(textarea);
  try {
    textarea.select();
    textarea.setSelectionRange(0, text.length);
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    textarea.remove();
    focused?.focus({ preventScroll: true });
  }
}

async function deleteImage(id) {
  if (!confirm("将这张图片移入回收站？在保留期内可以恢复。")) return;
  try {
    await request(`/api/admin/images/${id}`, { method: "DELETE" });
    showMessage(els.adminMessage, "图片已移入回收站");
    await Promise.all([loadImages(), loadTrash(), loadStorageOverview(), loadDashboard()]);
  } catch (err) {
    showMessage(els.adminMessage, err.message, false);
  }
}

function switchPanel(name) {
  if (!panelCopy[name]) name = "overview";
  $$(".admin-nav button").forEach((button) => button.classList.toggle("active", button.dataset.panel === name));
  $$(".admin-tab").forEach((panel) => {
    panel.hidden = panel.dataset.panel !== name;
  });
  els.panelTitle.textContent = panelCopy[name][0];
  els.panelSubtitle.textContent = panelCopy[name][1];
  history.replaceState(null, "", `#${name}`);
  if (name === "mail") window.OrimgMail?.load();
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[ch]);
}

function escapeAttr(value) {
  return escapeHTML(value).replace(/`/g, "&#096;");
}

function bindEvents() {
  els.loginForm.addEventListener("submit", login);
  els.refreshCaptcha.addEventListener("click", loadCaptcha);
  $("#retrySession").addEventListener("click", bootstrapAdmin);
  els.chooseLogoButton.addEventListener("click", () => els.logoFileInput.click());
  els.logoFileInput.addEventListener("change", (event) => uploadLogo(event.target.files[0] || null));
  els.siteForm.addEventListener("submit", (event) => {
    event.preventDefault();
    saveSettings(els.siteForm);
  });
  els.securityForm.addEventListener("submit", (event) => {
    event.preventDefault();
    saveSettings(els.securityForm);
  });
  els.storageForm.addEventListener("submit", (event) => {
    event.preventDefault();
    saveSettings(els.storageForm);
  });
  els.reloadDashboard.addEventListener("click", loadDashboard);
  els.reloadGroups.addEventListener("click", loadGroups);
  els.showGroupCreator.addEventListener("click", () => toggleGroupCreator(true));
  els.cancelGroupCreator.addEventListener("click", () => toggleGroupCreator(false));
  els.groupCreateForm.addEventListener("submit", createGroup);
  els.groupsGrid.addEventListener("click", (event) => {
    const entry=event.target.closest('.group-entry'); if(entry) openGroupEditor(entry);
  });
  els.groupsGrid.addEventListener('keydown',event=>{if((event.key==='Enter'||event.key===' ')&&event.target.matches('.group-summary')) {event.preventDefault();openGroupEditor(event.target.closest('.group-entry'));}});
  els.createUserButton.addEventListener("click", () => openUserEditor());
  els.userEditorForm.addEventListener("submit", saveUser);
  $("#guestGroupForm").addEventListener("submit", async event => {
    event.preventDefault();
    await confirmSave(event.currentTarget, "确认更改游客用户组？游客上传将立即应用该组策略，未分配则禁止上传。", async () => {
      await request("/api/admin/guest-group", {method:"PUT",headers:{"Content-Type":"application/json"},body:JSON.stringify({group_id:Number($("#guestGroupSelect").value)})});
      await loadGroups();
    }, "游客用户组已保存");
  });
  els.userEditorClose.addEventListener("click", closeUserEditor);
  els.userEditorCancel.addEventListener("click", closeUserEditor);
  els.userEditorBackdrop.addEventListener("click", closeUserEditor);
  els.userEditorDelete.addEventListener("click", () => deleteUser(els.userEditorForm.elements.id.value));
  els.reloadUsers.addEventListener("click", () => loadUsers(true));
  els.userFilterForm.addEventListener("submit", applyUserFilters);
  els.resetUserFilter.addEventListener("click", resetUserFilters);
  els.loadMoreUsers.addEventListener("click", () => loadUsers(false));
  els.reloadImages.addEventListener("click", () => loadImages(true));
  els.loadMoreAdminImages.addEventListener("click", () => loadImages(false));
  els.adminImageFilterForm.addEventListener("submit", applyAdminImageFilters);
  els.resetAdminImageFilter.addEventListener("click", resetAdminImageFilters);
  els.copyAdminSelected.addEventListener("click", copyAdminSelectedImages);
  els.deleteAdminSelected.addEventListener("click", deleteAdminSelectedImages);
  els.clearAdminSelection.addEventListener("click", () => {
    state.selectedImages.clear();
    renderAlbum(els.adminImages, state.adminImages);
    renderAdminImageSelection();
  });
  els.reloadAudit.addEventListener("click", () => loadAudit(true));
  els.loadMoreAudit.addEventListener("click", () => loadAudit(false));
  els.auditActionFilter.addEventListener("change", () => loadAudit(true));
  els.reloadTrash.addEventListener("click", loadTrash);
  els.reloadStorage.addEventListener("click", loadStorageOverview);
  els.runDatabaseBackup.addEventListener("click", runDatabaseBackup);
  els.storageJobs.addEventListener("click", (event) => {
    const button = event.target.closest("[data-job-action]");
    if (button) controlStorageJob(button.dataset.jobId, button.dataset.jobAction);
  });
  els.imageScope.addEventListener("change", () => loadImages(true));
  els.usersTable.addEventListener("click", (event) => {
    const edit = event.target.closest("[data-edit-user]");
    if (!edit) return;
    const user = state.users.find((item) => Number(item.id) === Number(edit.dataset.editUser));
    if (user) openUserEditor(user);
  });
  els.trashImages.addEventListener("click", (event) => {
    const restore = event.target.closest("[data-restore-image]");
    const purge = event.target.closest("[data-purge-image]");
    if (restore) restoreImage(restore.dataset.restoreImage);
    if (purge) purgeImage(purge.dataset.purgeImage);
  });
  $$(".admin-nav button").forEach((button) => button.addEventListener("click", () => switchPanel(button.dataset.panel)));
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !els.userEditor.hidden) closeUserEditor();
  });
  getAlbum();
}

bindEvents();
setInterval(() => {
  if (state.token && !els.adminLayout.hidden) loadStorageOverview();
}, 6000);
async function bootstrapAdmin() {
  $("#authStatus").hidden = false;
  $("#authStatusText").textContent = "正在确认登录状态";
  $("#retrySession").hidden = true;
  els.loginShell.hidden = true;
  els.adminLayout.hidden = true;
  await window.ZhuaBranding.load().catch(() => {});
  if (!state.token) { $("#authStatus").hidden = true; els.loginShell.hidden = false; loadCaptcha(); return; }
  try { await enterAdmin(); }
  catch (err) {
    if ([401, 403].includes(err.status)) logout();
    else { $("#authStatusText").textContent = "暂时无法连接，请重试"; $("#retrySession").hidden = false; }
  }
}
bootstrapAdmin();
