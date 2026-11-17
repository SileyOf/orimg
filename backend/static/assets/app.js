const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

const state = {
  settings: {},
  files: [],
  results: [],
  token: localStorage.getItem("zhuapic_token") || "",
  me: null,
	policy: null,
  ready: false,
};

const els = {
  siteName: $("#siteName"),
  siteTagline: $("#siteTagline"),
  storagePill: $("#storagePill"),
  announcement: $("#announcement"),
  heroTitle: $("#heroTitle"),
  heroSubtitle: $("#heroSubtitle"),
  footerText: $("#footerText"),
  maxUploadLabel: $("#maxUploadLabel"),
	batchLimitLabel: $("#batchLimitLabel"),
  accountLink: $("#accountLink"),
  adminStorageControl: $("#adminStorageControl"),
  storageSelect: $("#storageSelect"),
  dropzone: $("#dropzone"),
  dragScreen: $("#dragScreen"),
  fileInput: $("#fileInput"),
  fileMeta: $("#fileMeta"),
  uploadButton: $("#uploadButton"),
  uploadMessage: $("#uploadMessage"),
  queuePanel: $("#queuePanel"),
  queueTitle: $("#queueTitle"),
  queueSummary: $("#queueSummary"),
  queueList: $("#queueList"),
  clearQueueButton: $("#clearQueueButton"),
  batchCopy: $("#batchCopy"),
  copyFormat: $("#copyFormat"),
  copyAllButton: $("#copyAllButton"),
};

let dragDepth = 0;

async function request(path, options = {}) {
  const headers = options.headers || {};
  if (state.token) headers.Authorization = `Bearer ${state.token}`;
  const res = await fetch(path, { ...options, headers });
  let data = {};
  try {
    data = await res.json();
  } catch {
    data = {};
  }
  if (!res.ok) throw Object.assign(new Error(data.error || `HTTP ${res.status}`), { status: res.status });
  return data;
}

function setText(el, value) {
  if (el && value !== undefined && value !== null) el.textContent = value;
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

function showMessage(text, ok = true) {
  els.uploadMessage.textContent = text;
  els.uploadMessage.style.color = ok ? "var(--green)" : "#c9342c";
}

async function loadPublicSettings() {
  const settings = await window.ZhuaBranding.load();
  state.settings = settings;
  const brand = window.ZhuaBranding;
  brand.setText(els.siteTagline, brand.text(settings, "site_tagline"));
  brand.setText(els.announcement, brand.text(settings, "announcement"));
  brand.setText(els.heroTitle, brand.text(settings, "hero_title", brand.text(settings, "site_name")));
  brand.setText(els.heroSubtitle, brand.text(settings, "hero_subtitle"));
  brand.setText(els.footerText, brand.text(settings, "footer_text"));
  setText(els.maxUploadLabel, `${settings.max_upload_mb || 20}MB`);
  setText(els.batchLimitLabel, `${settings.upload_policy?.max_batch_count || 20}张`);
  setText(els.storagePill, settings.storage_mode || "local");
	state.policy = settings.upload_policy || null;
	applyPolicy(state.policy);
  if (els.storageSelect) els.storageSelect.value = "default";
  if (settings.user_path) {
    els.accountLink.href = settings.user_path;
  }
}

async function loadMe() {
  if (!state.token) return;
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 12000);
  try {
    const data = await request("/api/me", { signal: controller.signal });
		state.me = data.user;
		state.policy = data.upload_policy || state.policy;
		applyPolicy(state.policy);
    if (state.me) {
      els.accountLink.hidden = true;
      window.OrimgAccount.mount($("#accountSlot"), { user: state.me, userPath: state.settings.user_path, adminPath: state.settings.admin_path });
    }
    if (state.me?.role === "admin") {
      try {
        const data = await request("/api/admin/settings", { signal: controller.signal });
        window.OrimgAccount.mount($("#accountSlot"), { user: state.me, userPath: state.settings.user_path, adminPath: data.settings?.admin_path });
      } catch { /* The personal account remains usable if admin settings are unavailable. */ }
      els.accountLink.textContent = "控制台";
      els.accountLink.href = state.settings.user_path || "/dashboard";
    }
  } catch (err) {
    if (![401, 403].includes(err.status)) throw err;
    state.token = "";
    localStorage.removeItem("zhuapic_token");
  } finally { clearTimeout(timeout); }
}

async function loadUploadTargets() {
  const data = await request("/api/storage/options");
  const select = els.storageSelect;
  select.replaceChildren(new Option("默认 · " + data.default_name, "default"));
  for (const target of data.targets || []) {
    select.add(new Option(target.name + (target.is_default ? " · 默认" : ""), "target:" + target.id));
  }
  select.value = "default";
  els.adminStorageControl.hidden = !(data.targets || []).some(t => !t.is_default);
}

function applyPolicy(policy) {
	if (!policy) return;
	els.dropzone.setAttribute("aria-disabled", String(policy.active === false));
	if (policy.active === false) showMessage("未分配有效用户组或上传已停用，请联系管理员。", false);
	setText(els.maxUploadLabel, `${policy.max_file_mb || 20}MB`);
	setText(els.batchLimitLabel, `${policy.max_batch_count || 20}张`);
}

function imageFiles(fileList) {
  return Array.from(fileList || []).filter((file) => file.type.startsWith("image/") || /\.(avif|bmp|tiff?|ico|svg|heic|heif|jxl|psd)$/i.test(file.name));
}

function addFiles(fileList) {
  if (!state.ready) return;
	if (state.policy?.active === false) { showMessage("当前账号未分配有效用户组，或该组已停用上传。", false); return; }
	const policy = state.policy || {};
	const maxBatch = Number(policy.max_batch_count || 20);
	const maxBytes = Number(policy.max_file_mb || state.settings.max_upload_mb || 20) * 1024 * 1024;
	const available = Math.max(0, maxBatch - state.files.length);
	const allowed = new Set(String(policy.allowed_types || state.settings.allowed_types || "").toLowerCase().split(",").map((item) => item.trim().replace(/^\./, "")).filter(Boolean));
	const candidates = imageFiles(fileList);
	const disallowed = candidates.filter((file) => allowed.size && !allowed.has(file.name.split(".").pop().toLowerCase()));
	const permitted = candidates.filter((file) => !disallowed.includes(file));
	const oversized = permitted.filter((file) => file.size > maxBytes);
	const files = permitted.filter((file) => file.size <= maxBytes).slice(0, available);
	if (!files.length) {
		if (oversized.length) showMessage(`${oversized.length} 张图片超过 ${policy.max_file_mb || 20}MB 限制`, false);
		else if (disallowed.length) showMessage(`${disallowed.length} 张图片格式不在当前用户组允许范围内`, false);
		else if (available === 0) showMessage(`本组最多选择 ${maxBatch} 张图片`, false);
		return;
	}
  const next = files.map((file) => ({
    id: `${Date.now()}-${Math.random().toString(16).slice(2)}`,
    file,
    status: "ready",
    message: "等待上传",
    progress: 0,
    preview: URL.createObjectURL(file),
    result: null,
  }));
  state.files.push(...next);
  renderQueue();
	if (oversized.length) showMessage(`${oversized.length} 张图片超过 ${policy.max_file_mb || 20}MB 限制`, false);
	else if (disallowed.length) showMessage(`${disallowed.length} 张图片格式不在当前用户组允许范围内`, false);
	else if (candidates.length > files.length) showMessage(`本组最多选择 ${maxBatch} 张图片`, false);
	else showMessage(`已加入 ${files.length} 张图片`);
}

function renderQueue() {
  const total = state.files.length;
  const done = state.files.filter((item) => item.status === "done").length;
  const failed = state.files.filter((item) => item.status === "failed").length;
  const pending = state.files.filter((item) => item.status !== "done" && item.status !== "uploading").length;
  const uploading = state.files.some((item) => item.status === "uploading");
  const totalBytes = state.files.reduce((sum, item) => sum + item.file.size, 0);
  els.queuePanel.hidden = state.files.length === 0;
  document.body.classList.toggle("has-queue", total > 0);
  els.uploadButton.hidden = total < 2 || pending === 0 || uploading;
  els.uploadButton.disabled = pending === 0 || uploading;
  els.fileMeta.textContent = total ? `${total} 张图片 · ${prettyBytes(totalBytes)} · 点击此区域继续添加` : "支持批量选择与 Ctrl+V / Command+V 粘贴";
  setText(els.queueTitle, done ? "上传完成" : "上传队列");
  setText(els.queueSummary, total ? `${done}/${total} 完成 · ${failed ? `${failed} 失败 · ` : ""}${prettyBytes(totalBytes)}` : "等待开始");
  state.results = state.files.filter((item) => item.result).map((item) => item.result);
  els.batchCopy.hidden = state.results.length < 2;
  els.queueList.innerHTML = state.files
    .map((item) => `
      <article class="queue-item ${item.status}" data-queue-id="${escapeAttr(item.id)}" style="--progress:${item.progress || 0}%">
        <img class="queue-thumb" src="${escapeAttr(item.preview)}" alt="" />
        <div class="queue-main">
          <strong>${escapeHTML(resultName(item))}</strong>
          <small>${prettyBytes(item.file.size)} · ${escapeHTML(item.message)}</small>
          ${item.result ? `<span class="queue-url">${escapeHTML(item.result.url)}</span>` : ""}
        </div>
        <div class="queue-actions">
          ${item.result ? copyMenu(item.result) : total === 1 && item.status !== "uploading" ? `<button class="primary-button compact" data-upload-one="${escapeAttr(item.id)}" type="button">${item.status === "failed" ? "重试" : "上传"}</button>` : `<span class="queue-state">${statusText(item)}</span>`}
          <button class="icon-button small" data-remove="${escapeAttr(item.id)}" type="button" aria-label="移除" ${item.status === "uploading" ? "disabled" : ""}>×</button>
        </div>
      </article>
    `)
    .join("");
}

async function runUploads(items) {
  if (!items.length || state.files.some((item) => item.status === "uploading")) return;
  document.body.classList.add("uploading");
  const destination = els.storageSelect?.value || "default";
  if (els.storageSelect) els.storageSelect.disabled = true;
	let cursor = 0;
	const uploadNext = async () => {
		while (cursor < items.length) {
			const item = items[cursor];
			cursor += 1;
    item.status = "uploading";
    item.message = "上传中";
		item.progress = 1;
		item.progressTarget = 1;
    renderQueue();
    try {
      const form = new FormData();
      form.append("file", item.file);
      form.append("storage_mode", destination);
      const data = localizeUploadResult(await uploadRequest(item, form));
		await finishItemProgress(item);
      item.status = "done";
      item.message = "完成";
      item.progress = 100;
      item.result = data;
    } catch (err) {
      item.status = "failed";
      item.message = err.message;
    }
    renderQueue();
		}
	};
	const workerCount = Math.min(3, items.length);
	await Promise.all(Array.from({ length: workerCount }, () => uploadNext()));
  if (els.storageSelect) els.storageSelect.disabled = false;
  document.body.classList.remove("uploading");
  renderQueue();
  const failed = state.files.filter((item) => item.status === "failed").length;
  showMessage(failed ? `上传完成，${failed} 张失败` : "全部上传完成", failed === 0);
}

async function uploadQueue() {
  await runUploads(state.files.filter((item) => item.status !== "done" && item.status !== "uploading"));
}

function uploadRequest(item, form) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/upload");
    if (state.token) xhr.setRequestHeader("Authorization", `Bearer ${state.token}`);
    xhr.upload.addEventListener("progress", (event) => {
      if (!event.lengthComputable) return;
      const ratio = event.loaded / event.total;
      item.message = ratio >= 0.999 ? "处理中" : "上传中";
		setProgressTarget(item, Math.min(96, Math.max(2, ratio * 96)));
    });
    xhr.addEventListener("load", () => {
      let data = {};
      try {
        data = JSON.parse(xhr.responseText || "{}");
      } catch {
        data = {};
      }
      if (xhr.status >= 200 && xhr.status < 300) resolve(data);
      else reject(new Error(data.error || `HTTP ${xhr.status}`));
    });
    xhr.addEventListener("error", () => reject(new Error("上传连接中断")));
    xhr.addEventListener("abort", () => reject(new Error("上传已取消")));
    xhr.send(form);
  });
}

function setProgressTarget(item, target) {
	item.progressTarget = Math.max(item.progressTarget || 0, target);
	if (item.progressFrame) return;
	const tick = () => {
		const distance = (item.progressTarget || 0) - (item.progress || 0);
		if (distance <= 0.08) {
			item.progress = item.progressTarget;
			setItemProgress(item, item.progress);
			item.progressFrame = 0;
			return;
		}
		item.progress += Math.max(0.12, distance * 0.14);
		setItemProgress(item, item.progress);
		item.progressFrame = requestAnimationFrame(tick);
	};
	item.progressFrame = requestAnimationFrame(tick);
}

function finishItemProgress(item) {
	setProgressTarget(item, 100);
	return new Promise((resolve) => {
		const waitForProgress = () => {
			if ((item.progress || 0) >= 99.9) return resolve();
			requestAnimationFrame(waitForProgress);
		};
		waitForProgress();
	});
}

function setItemProgress(item, progress) {
  item.progress = Math.max(item.progress || 0, Math.min(100, progress));
  const row = els.queueList.querySelector(`[data-queue-id="${item.id}"]`);
  if (!row) return;
  row.style.setProperty("--progress", `${item.progress}%`);
  const stateLabel = row.querySelector(".queue-state");
  if (stateLabel) stateLabel.textContent = `${Math.round(item.progress)}%`;
  const meta = row.querySelector(".queue-main small");
  if (meta) meta.textContent = `${prettyBytes(item.file.size)} · ${item.message} ${Math.round(item.progress)}%`;
}

function localizeUploadResult(data) {
  const localizedURL = currentOriginURL(data.url);
  data.url = localizedURL;
  data.markdown = `![](${localizedURL})`;
  data.html = `<img src="${localizedURL}" alt="">`;
  if (data.image) {
    for (const key of ["url", "thumb_small", "thumb_medium", "thumb_large"]) {
      if (data.image[key]) data.image[key] = currentOriginURL(data.image[key]);
    }
  }
  return data;
}

function currentOriginURL(value) {
  try {
    const parsed = new URL(value, location.origin);
    if (!parsed.pathname.startsWith("/i/") && !parsed.pathname.startsWith("/t/")) return value;
    return `${location.origin}${parsed.pathname}${parsed.search}`;
  } catch {
    return value;
  }
}

function wait(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function formatResults(format) {
  return state.results
    .map((item) => {
      const url = item.url;
      if (format === "markdown") return `![](${url})`;
      if (format === "html") return `<img src="${url}" alt="">`;
      if (format === "bbcode") return `[img]${url}[/img]`;
      return url;
    })
    .join("\n");
}

async function copyAll() {
  const value = formatResults(els.copyFormat.value);
  if (!value) return;
  try {
    await writeClipboard(value);
    showMessage("已复制全部");
  } catch {
    showMessage("复制失败，请手动选择链接", false);
  }
}

function resultName(item) {
  return item.result?.image?.original_name || item.file.name;
}

function statusText(item) {
  if (item.status === "uploading") return `${Math.round(item.progress || 0)}%`;
  if (item.status === "failed") return "失败";
  if (item.status === "done") return "完成";
  return "待上传";
}

function copyMenu(item) {
  const encoded = encodeURIComponent(JSON.stringify({
    url: item.url,
    markdown: item.markdown || `![](${item.url})`,
    html: item.html || `<img src="${item.url}" alt="">`,
    bbcode: `[img]${item.url}[/img]`,
  }));
  return `
    <div class="copy-control" data-copy-pack="${encoded}">
      <button class="copy-primary" data-copy-one="url" type="button">复制链接</button>
      <details class="copy-menu">
        <summary title="更多复制格式" aria-label="更多复制格式">…</summary>
        <div>
          <button data-copy-one="markdown" type="button">Markdown</button>
          <button data-copy-one="html" type="button">HTML</button>
          <button data-copy-one="bbcode" type="button">BBCode</button>
        </div>
      </details>
    </div>
  `;
}

async function copyOne(button) {
  const pack = button.closest("[data-copy-pack]");
  if (!pack) return;
  try {
    const data = JSON.parse(decodeURIComponent(pack.dataset.copyPack));
    await writeClipboard(data[button.dataset.copyOne] || data.url);
    pack.querySelector("details")?.removeAttribute("open");
    showMessage(button.dataset.copyOne === "url" ? "链接已复制" : `${button.textContent} 已复制`);
  } catch {
    showMessage("复制失败，请手动选择链接", false);
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
  document.body.appendChild(textarea);
  textarea.select();
  textarea.setSelectionRange(0, text.length);
  const copied = document.execCommand("copy");
  textarea.remove();
  return copied;
}

function setDragging(active) {
  if (!state.ready) active = false;
  document.body.classList.toggle("drag-active", active);
  els.dropzone.classList.toggle("dragging", active);
  els.dragScreen.hidden = !active;
}

function bindDragAndDrop() {
  window.addEventListener("dragenter", (event) => {
    event.preventDefault();
    dragDepth += 1;
    setDragging(true);
  });
  window.addEventListener("dragover", (event) => {
    event.preventDefault();
    setDragging(true);
  });
  window.addEventListener("dragleave", (event) => {
    event.preventDefault();
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) setDragging(false);
  });
  window.addEventListener("drop", (event) => {
    event.preventDefault();
    dragDepth = 0;
    setDragging(false);
    addFiles(event.dataTransfer.files);
  });
}

function bindPaste() {
  window.addEventListener("paste", (event) => {
    const files = Array.from(event.clipboardData?.items || [])
      .filter((item) => item.kind === "file")
      .map((item) => item.getAsFile())
      .filter(Boolean);
    addFiles(files);
  });
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#039;" })[ch]);
}

function escapeAttr(value) {
  return escapeHTML(value).replace(/`/g, "&#096;");
}

function bindEvents() {
  els.dragScreen.hidden = true;
  els.dropzone.addEventListener("click", (event) => {
    if (event.target.closest("button")) return;
    if (state.policy?.active === false) return;
    els.fileInput.click();
  });
  els.dropzone.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    event.preventDefault();
    if (state.policy?.active === false) return;
    els.fileInput.click();
  });
  els.fileInput.addEventListener("change", (event) => {
    addFiles(event.target.files);
    event.target.value = "";
  });
  els.uploadButton.addEventListener("click", uploadQueue);
  els.clearQueueButton.addEventListener("click", () => {
    state.files.forEach((item) => URL.revokeObjectURL(item.preview));
    state.files = [];
    state.results = [];
    renderQueue();
    showMessage("");
  });
  els.queueList.addEventListener("click", (event) => {
    const upload = event.target.closest("[data-upload-one]");
    if (upload) {
      const item = state.files.find((entry) => entry.id === upload.dataset.uploadOne);
      if (item) runUploads([item]);
      return;
    }
    const button = event.target.closest("[data-remove]");
    if (!button) return;
    const item = state.files.find((entry) => entry.id === button.dataset.remove);
    if (item) URL.revokeObjectURL(item.preview);
    state.files = state.files.filter((item) => item.id !== button.dataset.remove);
    renderQueue();
  });
  els.copyAllButton.addEventListener("click", copyAll);
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-copy-one]");
    if (button) copyOne(button);
  });
  bindDragAndDrop();
  bindPaste();
}

bindEvents();
let loadingSite = false;
async function initializeSite() {
  if (loadingSite) return;
  loadingSite = true;
  document.body.dataset.siteState = "loading";
  $("#retrySiteLoad").hidden = true;
  $("#siteLoadMessage").textContent = "正在加载";
  try {
    await loadPublicSettings();
    await loadMe();
    await loadUploadTargets();
    state.ready = true;
    document.body.dataset.siteState = "ready";
    $("#sitePage").inert = false;
    $("#sitePage").setAttribute("aria-busy", "false");
    $("#siteLoadState").hidden = true;
  } catch {
    document.body.dataset.siteState = "error";
    $("#siteLoadMessage").textContent = "暂时无法加载站点，请重试";
    $("#retrySiteLoad").hidden = false;
  } finally { loadingSite = false; }
}
$("#retrySiteLoad").addEventListener("click", initializeSite);
initializeSite();
