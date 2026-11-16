const $ = (selector) => document.querySelector(selector);

const state = {
  token: localStorage.getItem("zhuapic_token") || "",
  captchaToken: "",
  me: null,
	policy: null,
	usage: null,
	albumImages: [],
	imageCursor: "",
  selectedImages: new Set(),
  imageFilters: { q: "", storage: "", from: "", to: "" },
  apiTokens: [],
};

const els = {
  loginShell: $("#loginShell"),
  loginForm: $("#loginForm"),
  loginMessage: $("#loginMessage"),
  captchaQuestion: $("#captchaQuestion"),
  refreshCaptcha: $("#refreshCaptcha"),
  dashboardShell: $("#dashboardShell"),
  dashboardName: $("#dashboardName"),
  policyGroup: $("#policyGroup"),
  policyLimits: $("#policyLimits"),
  policyQuota: $("#policyQuota"),
  policyQuotaBar: $("#policyQuotaBar"),
  profileForm: $("#profileForm"),
  profileMessage: $("#profileMessage"),
  albumGrid: $("#albumGrid"),
  albumMessage: $("#albumMessage"),
  reloadImages: $("#reloadImages"),
	loadMoreImages: $("#loadMoreImages"),
  reloadUserOverview: $("#reloadUserOverview"),
  userMetricImages: $("#userMetricImages"),
  userMetricBytes: $("#userMetricBytes"),
  userMetricToday: $("#userMetricToday"),
  userMetricTodayBytes: $("#userMetricTodayBytes"),
  userMetricLocal: $("#userMetricLocal"),
  userMetricCloud: $("#userMetricCloud"),
  userMetricMirror: $("#userMetricMirror"),
  userActivityChart: $("#userActivityChart"),
  overviewStorageMode: $("#overviewStorageMode"),
  imageFilterForm: $("#imageFilterForm"),
  imageSearch: $("#imageSearch"),
  imageStorageFilter: $("#imageStorageFilter"),
  imageFromFilter: $("#imageFromFilter"),
  imageToFilter: $("#imageToFilter"),
  resetImageFilter: $("#resetImageFilter"),
  imageSelectionBar: $("#imageSelectionBar"),
  selectedImageCount: $("#selectedImageCount"),
  copySelectedImages: $("#copySelectedImages"),
  deleteSelectedImages: $("#deleteSelectedImages"),
  clearImageSelection: $("#clearImageSelection"),
  tokenCreateForm: $("#tokenCreateForm"),
  tokenReveal: $("#tokenReveal"),
  tokenValue: $("#tokenValue"),
  copyToken: $("#copyToken"),
  tokenList: $("#tokenList"),
  tokenMessage: $("#tokenMessage"),
};

let album;
let imageRequestVersion = 0;
let imagesLoading = false;
function getAlbum() {
  if (!album) album = new ZhuaAlbum({
    container: els.albumGrid, filterForm: els.imageFilterForm, selectionBar: els.imageSelectionBar,
    selected: state.selectedImages, onSelection: renderImageSelection, onDelete: deleteImage, writeClipboard,
  });
  return album;
}

async function request(path, options = {}) {
  const headers = options.headers || {};
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  const res = await fetch(path, { ...options, headers, signal: options.signal || AbortSignal.timeout(12000) });
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
    await bootstrapSession();
  } catch (err) {
    showMessage(els.loginMessage, err.message, false);
    await loadCaptcha();
  }
}

async function loadPublicSettings() {
  try {
    await window.ZhuaBranding.load();
  } catch { /* Keep neutral branding when public settings are unavailable. */ }
}

function openAccount() {
  const dialog = $("#accountDialog");
  if (!dialog.open) dialog.showModal();
}

function mountAccount() {
  window.OrimgAccountMail?.refresh(state.me);
  const avatar = $("#profileAvatar");
  avatar.textContent = Array.from(state.me.username || "O")[0].toUpperCase();
  $("#removeAvatar").hidden = !state.me.avatar_url;
  if (/^\/avatars\/[a-f0-9]{32}\.png$/.test(state.me.avatar_url || "")) {
    const img = document.createElement("img"); img.src = state.me.avatar_url; img.alt = ""; avatar.replaceChildren(img);
  }
  window.OrimgAccount.mount($("#accountSlot"), {
    user: state.me, adminPath: state.adminPath || "/admin", onSettings: openAccount,
    onLogout: () => { location.reload(); },
  });
  $("#dashboardGreeting").textContent = state.me.username ? `${state.me.username} 的空间` : "我的空间";
}

async function enterDashboard() {
  const data = await request("/api/me");
  if (!data.user) throw new Error("账号信息暂时不可用");
  state.me = data.user;
  state.policy = data.upload_policy || null;
  state.usage = data.upload_usage || null;
  renderPolicySummary();
  els.profileForm.elements.username.value = state.me.username || "";
  els.profileForm.elements.email.value = state.me.email || "";
  mountAccount();
  const requestedPage = location.hash.slice(1);
  switchPage(requestedPage);
  $("#authStatus").hidden = true;
  els.loginShell.hidden = true;
  els.dashboardShell.hidden = false;
  if (["profile", "account"].includes(requestedPage)) openAccount();
  if (state.me.role === "admin") {
    request("/api/admin/settings").then((data) => {
      state.adminPath = data.settings?.admin_path || "/admin";
      mountAccount();
    }).catch(() => {});
  }
  await Promise.all([
    loadImages().catch((err) => showMessage(els.albumMessage, err.message, false)),
    refreshOverview(),
    loadTokens(),
  ]);
}

async function bootstrapSession() {
  $("#authStatus").hidden = false;
  $("#authStatusText").textContent = "正在确认登录状态";
  $("#retrySession").hidden = true;
  els.loginShell.hidden = true;
  els.dashboardShell.hidden = true;
  await loadPublicSettings();
  if (!state.token) { showLogin(); return; }
  try { await enterDashboard(); }
  catch (err) {
    if ([401, 403].includes(err.status)) {
      state.token = "";
      localStorage.removeItem("zhuapic_token");
      showLogin();
    } else {
      $("#authStatusText").textContent = "暂时无法连接，请重试";
      $("#retrySession").hidden = false;
    }
  }
}

function showLogin() {
  $("#authStatus").hidden = true;
  els.loginShell.hidden = false;
  loadCaptcha();
}

async function refreshOverview() {
  const button = els.reloadUserOverview;
  button.disabled = true;
  $("#overviewMessage").textContent = "";
  try { await loadUserOverview(); }
  catch (err) { showMessage($("#overviewMessage"), "仪表盘暂时无法加载，请刷新重试。", false); }
  finally { button.disabled = false; }
}

function renderPolicySummary() {
	const policy = state.policy || {};
	const usage = state.usage || {};
	const used = Number(usage.total_bytes || 0);
	const quota = Number(policy.total_quota_bytes || 0);
	els.policyGroup.textContent = state.me?.group_name || policy.name || "无用户组";
	els.policyLimits.textContent = policy.active === false ? "不可上传" : `${policy.max_file_mb || 20}MB / ${policy.max_batch_count || 20}张`;
	els.policyQuota.textContent = policy.active === false ? `${prettyBytes(used)} · 上传已停用` : (quota > 0 ? `${prettyBytes(used)} / ${prettyBytes(quota)}` : `${prettyBytes(used)} / 不限`) + (state.me?.quota_bytes != null ? " · 个人容量" : "");
	els.policyQuotaBar.style.width = `${quota > 0 ? Math.min(100, (used / quota) * 100) : 0}%`;
	els.overviewStorageMode.textContent = !policy.storage_mode || policy.storage_mode === "default" ? "跟随站点" : (policy.storage_name || { local: "Local", s3: "S3", mirror: "Local + S3" }[policy.storage_mode] || "指定存储");
}

async function loadUserOverview() {
  const data = await request("/api/user/dashboard");
  const metrics = data.metrics || {};
  els.userMetricImages.textContent = Number(metrics.images || 0).toLocaleString("zh-CN");
  els.userMetricBytes.textContent = prettyBytes(metrics.image_bytes || 0);
  els.userMetricToday.textContent = Number(metrics.uploads_today || 0).toLocaleString("zh-CN");
  els.userMetricTodayBytes.textContent = prettyBytes(metrics.bytes_today || 0);
  els.userMetricLocal.textContent = Number(metrics.local_images || 0).toLocaleString("zh-CN");
  els.userMetricCloud.textContent = Number((metrics.s3_images || 0) + (metrics.mirror_images || 0)).toLocaleString("zh-CN");
  els.userMetricMirror.textContent = `${Number(metrics.mirror_images || 0).toLocaleString("zh-CN")} 张 Mirror`;
  renderUserActivity(data.activity || []);
}

function renderUserActivity(activity) {
  const byDate = new Map(activity.map((item) => [item.date, item]));
  const days = [];
  for (let offset = 13; offset >= 0; offset -= 1) {
    const date = new Date();
    date.setHours(12, 0, 0, 0);
    date.setDate(date.getDate() - offset);
    const key = `${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
    days.push(byDate.get(key) || { date: key, count: 0, bytes: 0 });
  }
  const max = Math.max(1, ...days.map((item) => Number(item.count || 0)));
  els.userActivityChart.innerHTML = days.map((item) => `
    <div class="activity-column" title="${item.count || 0} 张 · ${prettyBytes(item.bytes || 0)}">
      <div><i style="height:${Math.round((Number(item.count || 0) / max) * 100)}%"></i></div>
      <strong>${Number(item.count || 0)}</strong><span>${escapeHTML(item.date)}</span>
    </div>`).join("");
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

async function updateAvatar(file) {
  const controls = [$("#chooseAvatar"), $("#removeAvatar")];
  if (controls.some(button => button.disabled)) return;
  controls.forEach(button => button.disabled = true);
  try {
    let data;
    if (file) {
      if (!["image/png", "image/jpeg", "image/webp"].includes(file.type) || file.size > 8 * 1024 * 1024) throw new Error("请选择 8 MB 以内的 PNG、JPEG 或 WebP");
      const bitmap = await createImageBitmap(file);
      try {
        const canvas = document.createElement("canvas"); canvas.width = canvas.height = 256;
        const context = canvas.getContext("2d");
        const edge = Math.min(bitmap.width, bitmap.height);
        context.drawImage(bitmap, (bitmap.width-edge)/2, (bitmap.height-edge)/2, edge, edge, 0, 0, 256, 256);
        const blob = await new Promise(resolve => canvas.toBlob(resolve, "image/png"));
        if (!blob) throw new Error("头像处理失败");
        const body = new FormData(); body.append("avatar", blob, "avatar.png");
        data = await request("/api/me/avatar", {method:"POST",body});
      } finally { bitmap.close(); }
    } else data = await request("/api/me/avatar", {method:"DELETE"});
    state.me.avatar_url = data.avatar_url || ""; mountAccount();
    showMessage(els.profileMessage, file ? "头像已更新" : "头像已移除");
  } catch (err) { showMessage(els.profileMessage, err.message, false); }
  finally { controls.forEach(button => button.disabled = false); $("#avatarFile").value = ""; }
}

async function saveProfile(event) {
  event.preventDefault();
  const form = new FormData(els.profileForm);
  try {
    const data = await request("/api/me", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        username: form.get("username"),
        email: form.get("email"),
        current_password: form.get("current_password"),
        new_password: form.get("new_password"),
      }),
    });
    if (data.requires_login) {
      localStorage.removeItem("zhuapic_token");
      state.token = "";
      showLogin();
      showMessage(els.loginMessage, "密码已更新，请重新登录");
      return;
    }
    state.me = data.user;
    mountAccount();
    els.profileForm.elements.current_password.value = "";
    els.profileForm.elements.new_password.value = "";
    showMessage(els.profileMessage, "账号已保存");
  } catch (err) {
    showMessage(els.profileMessage, err.message, false);
  }
}

async function loadTokens() {
  if (!state.policy?.allow_api) {
    els.tokenCreateForm.hidden = true;
    els.tokenList.innerHTML = '<div class="token-empty">当前用户组未开放 API 上传。</div>';
    return;
  }
  els.tokenCreateForm.hidden = false;
  try {
    const data = await request("/api/user/tokens");
    state.apiTokens = data.tokens || [];
    renderTokens();
  } catch (err) {
    showMessage(els.tokenMessage, err.message, false);
  }
}

function renderTokens() {
  if (!state.apiTokens.length) {
    els.tokenList.innerHTML = '<div class="token-empty">尚未创建 API Token。</div>';
    return;
  }
  els.tokenList.innerHTML = state.apiTokens.map((token) => {
    const expiry = token.expires_at ? new Date(token.expires_at).toLocaleDateString("zh-CN") : "不过期";
    const lastUsed = token.last_used_at ? new Date(token.last_used_at).toLocaleString("zh-CN") : "从未使用";
    return `<article class="token-row">
      <div><strong>${escapeHTML(token.name)}</strong><code>${escapeHTML(token.token_prefix)}…</code></div>
      <div><span>到期 ${escapeHTML(expiry)}</span><small>${escapeHTML(lastUsed)}</small></div>
      <button class="danger-button compact" data-revoke-token="${token.id}" type="button">撤销</button>
    </article>`;
  }).join("");
}

async function createToken(event) {
  event.preventDefault();
  const form = new FormData(els.tokenCreateForm);
  try {
    const data = await request("/api/user/tokens", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: form.get("name"), expires_days: Number(form.get("expires_days")) }),
    });
    els.tokenValue.textContent = data.token;
    els.tokenReveal.hidden = false;
    els.tokenCreateForm.reset();
    showMessage(els.tokenMessage, "Token 已创建，请立即保存");
    await loadTokens();
  } catch (err) {
    showMessage(els.tokenMessage, err.message, false);
  }
}

async function revokeToken(id) {
  if (!confirm("撤销后使用该 Token 的客户端将立即无法上传，确定继续？")) return;
  try {
    await request(`/api/user/tokens/${id}`, { method: "DELETE" });
    showMessage(els.tokenMessage, "Token 已撤销");
    await loadTokens();
  } catch (err) {
    showMessage(els.tokenMessage, err.message, false);
  }
}

async function loadImages(reset = true) {
  if (!reset && imagesLoading) return;
  const version = ++imageRequestVersion;
  imagesLoading = true;
  els.loadMoreImages.disabled = true;
  els.reloadImages.disabled = true;
  els.albumGrid.setAttribute("aria-busy", "true");
  try {
    const params = new URLSearchParams({ limit: "60", scope: "mine" });
    if (!reset && state.imageCursor) params.set("cursor", state.imageCursor);
    for (const [key, value] of Object.entries(state.imageFilters)) if (value) params.set(key, value);
    const data = await request(`/api/user/images?${params.toString()}`);
    if (version !== imageRequestVersion) return;
    state.albumImages = reset ? (data.images || []) : Array.from(new Map([...state.albumImages, ...(data.images || [])].map((image) => [image.id, image])).values());
    if (reset) state.selectedImages.clear();
    state.imageCursor = data.next_cursor || "";
    els.loadMoreImages.hidden = !data.has_more;
    renderAlbum(state.albumImages);
    if (reset && !Object.values(state.imageFilters).some(Boolean)) renderRecentImages(state.albumImages);
    renderImageSelection();
  } finally {
    if (version === imageRequestVersion) {
      imagesLoading = false;
      els.loadMoreImages.disabled = false;
      els.reloadImages.disabled = false;
      els.albumGrid.setAttribute("aria-busy", "false");
    }
  }
}
function renderRecentImages(images) {
  const container = $("#recentImages");
  container.replaceChildren();
  if (!images.length) {
    const empty = document.createElement("p"); empty.className = "recent-empty"; empty.textContent = "还没有图片";
    const link = document.createElement("a"); link.href = "/"; link.textContent = "上传第一张图片"; link.className = "text-action";
    container.append(empty, link); return;
  }
  for (const item of images.slice(0, 6)) {
    const button = document.createElement("button"); button.type = "button"; button.className = "recent-image";
    const img = document.createElement("img"); try { const url = new URL(item.thumb_medium || item.thumb_small || item.url, location.origin); if (/^https?:$/.test(url.protocol)) img.src = url.pathname.startsWith("/i/") ? location.origin + url.pathname + url.search : url.href; } catch {} img.alt = ""; img.loading = "lazy";
    const name = document.createElement("span"); name.textContent = item.original_name || item.filename || item.id;
    button.title = name.textContent; button.append(img, name);
    button.addEventListener("click", () => {
      switchPage("photos");
      const tile = [...els.albumGrid.querySelectorAll("[data-ag-open]")].find((el) => el.dataset.agOpen === String(item.id));
      tile?.focus();
      tile?.click();
    });
    container.append(button);
  }
}

function renderAlbum(images) {
  getAlbum().setImages(images);
}

function renderImageSelection() {
  const count = state.selectedImages.size;
  els.imageSelectionBar.hidden = count === 0;
  els.selectedImageCount.textContent = String(count);
  album?.syncSelection();
}

async function copySelectedImageLinks() {
  await getAlbum().copySelected();
}

async function deleteSelectedImageRows() {
  const ids = Array.from(state.selectedImages);
  if (ids.length > 100) { getAlbum().announce("每次最多删除 100 张图片，请减少选择", true); return; }
  if (!ids.length || !confirm(`将选中的 ${ids.length} 张图片移至回收站？`)) return;
  try {
    const data = await request("/api/user/images/bulk", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action: "delete", ids }),
    });
    showMessage(els.albumMessage, `${data.deleted || 0} 张图片已移至回收站`);
    await Promise.all([loadImages(true), loadUserOverview()]);
  } catch (err) {
    showMessage(els.albumMessage, err.message, false);
  }
}

function applyImageFilters(event) {
  event.preventDefault();
  state.imageFilters = {
    q: els.imageSearch.value.trim(),
    storage: els.imageStorageFilter.value,
    from: els.imageFromFilter.value,
    to: els.imageToFilter.value,
  };
  loadImages(true).catch((err) => showMessage(els.albumMessage, err.message, false));
}

function resetImageFilters() {
  els.imageFilterForm.reset();
  state.imageFilters = { q: "", storage: "", from: "", to: "" };
  loadImages(true).catch((err) => showMessage(els.albumMessage, err.message, false));
}

async function deleteImage(id) {
  if (!confirm("删除这张图片？")) return;
  try {
    await request(`/api/user/images/${id}`, { method: "DELETE" });
    await loadImages();
  } catch (err) {
    showMessage(els.albumMessage, err.message, false);
  }
}

function switchPage(page) {
  if (!["overview", "photos"].includes(page)) page = "overview";
  document.querySelectorAll(".dashboard-nav button").forEach((button) => button.classList.toggle("active", button.dataset.page === page));
  document.querySelectorAll(".dashboard-page-panel").forEach((panel) => {
    panel.hidden = panel.dataset.page !== page;
  });
  history.replaceState(null, "", `#${page}`);
}

function bindEvents() {
  els.loginForm.addEventListener("submit", login);
  els.refreshCaptcha.addEventListener("click", loadCaptcha);
  $("#retrySession").addEventListener("click", bootstrapSession);
  $("#viewAllPhotos").addEventListener("click", () => switchPage("photos"));
  $("#closeAccount").addEventListener("click", () => $("#accountDialog").close());
  $("#accountDialog").addEventListener("click", (event) => {
    if (event.target !== event.currentTarget) return;
    const rect = event.currentTarget.getBoundingClientRect();
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) event.currentTarget.close();
  });
  $("#accountDialog").addEventListener("close", () => {
    els.profileForm.elements.current_password.value = "";
    els.profileForm.elements.new_password.value = "";
    els.tokenValue.textContent = "";
    els.tokenReveal.hidden = true;
  });
  window.addEventListener("hashchange", () => {
    if (!state.me) return;
    if (["account", "profile"].includes(location.hash.slice(1))) openAccount();
    else switchPage(location.hash.slice(1));
  });
  els.profileForm.addEventListener("submit", saveProfile);
  $("#chooseAvatar").addEventListener("click", () => $("#avatarFile").click());
  $("#avatarFile").addEventListener("change", event => updateAvatar(event.target.files[0]));
  $("#removeAvatar").addEventListener("click", () => { if (confirm("移除自定义头像？")) updateAvatar(null); });
	els.reloadImages.addEventListener("click", () => loadImages(true).catch((err) => showMessage(els.albumMessage, err.message, false)));
	els.loadMoreImages.addEventListener("click", () => loadImages(false).catch((err) => showMessage(els.albumMessage, err.message, false)));
  els.reloadUserOverview.addEventListener("click", refreshOverview);
  els.imageFilterForm.addEventListener("submit", applyImageFilters);
  els.resetImageFilter.addEventListener("click", resetImageFilters);
  els.copySelectedImages.addEventListener("click", copySelectedImageLinks);
  els.deleteSelectedImages.addEventListener("click", deleteSelectedImageRows);
  els.clearImageSelection.addEventListener("click", () => {
    state.selectedImages.clear();
    renderAlbum(state.albumImages);
    renderImageSelection();
  });
  els.tokenCreateForm.addEventListener("submit", createToken);
  els.copyToken.addEventListener("click", async () => {
    try {
      await writeClipboard(els.tokenValue.textContent);
      showMessage(els.tokenMessage, "Token 已复制");
    } catch {
      showMessage(els.tokenMessage, "复制失败", false);
    }
  });
  els.tokenList.addEventListener("click", (event) => {
    const button = event.target.closest("[data-revoke-token]");
    if (button) revokeToken(button.dataset.revokeToken);
  });
  document.querySelectorAll(".dashboard-nav button").forEach((button) => button.addEventListener("click", () => switchPage(button.dataset.page)));
  getAlbum();
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[ch]);
}

bindEvents();
bootstrapSession();
