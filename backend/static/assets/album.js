/* Shared gallery and viewer. No album data or account settings are stored locally. */
(() => {
  const escape = (value) => String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  const icon = (name) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${(window.AlbumIcons[name] || []).map(([tag, attrs]) => `<${tag} ${Object.entries(attrs).map(([k, v]) => `${k}="${escape(v)}"`).join(" ")}/>`).join("")}</svg>`;
  const button = (action, name, title, extra = "") => `<button type="button" class="ag-icon" data-ag="${action}" title="${title}" aria-label="${title}" ${extra}>${icon(name)}</button>`;
  const formats = `<option value="url">URL</option><option value="markdown">Markdown</option><option value="html">HTML</option><option value="bbcode">BBCode</option>`;
  const bytes = (n) => n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.ceil((n || 0) / 1024)} KB`;
  const safeURL = (value) => {
    try { const url = new URL(value, location.origin); return /^https?:$/.test(url.protocol) ? url.href : ""; } catch { return ""; }
  };
  const imageURL = (image) => {
    const url = safeURL(image.url);
    if (!url) return "";
    const parsed = new URL(url);
    return parsed.pathname.startsWith("/i/") ? `${location.origin}${parsed.pathname}${parsed.search}` : url;
  };
  const code = (image, format) => {
    const url = imageURL(image);
    const name = image.original_name || image.public_name || "image";
    if (format === "markdown") return `![${name.replace(/[\\\[\]]/g, "\\$&").replace(/[\r\n]/g, " ")}](${url.replace(/[()]/g, (c) => c === "(" ? "%28" : "%29")})`;
    if (format === "html") return `<img src="${escape(url)}" alt="${escape(name)}" />`;
    if (format === "bbcode") return `[img]${url.replace(/\[/g, "%5B").replace(/\]/g, "%5D")}[/img]`;
    return url;
  };

  class ZhuaAlbum {
    constructor(options) {
      Object.assign(this, options);
      this.images = [];
      this.index = 0;
      this.lastSelected = null;
      this.pointers = new Map();
      let saved = {};
      try { saved = JSON.parse(localStorage.getItem("zhuapic_album_view") || "{}"); } catch {}
      this.size = Math.max(120, Math.min(260, Number(saved.size) || 180));
      this.fit = saved.fit === "contain" ? "contain" : "cover";
      this.format = ["url", "markdown", "html", "bbcode"].includes(saved.format) ? saved.format : "url";
      this.panel = this.container.closest("section.admin-panel");
      this.panel.classList.add("ag-panel");
      this.showOwner = this.panel.dataset.panel === "images";
      const refresh = this.panel.querySelector("#reloadImages");
      refresh.className = "ag-icon";
      refresh.innerHTML = icon("RefreshCw");
      refresh.setAttribute("aria-label", this.showOwner ? "刷新图库" : "刷新相册");
      refresh.title = this.showOwner ? "刷新图库" : "刷新相册";
      this.container.classList.add("ag-gallery");
      this.toolbar = document.createElement("div");
      this.toolbar.className = "ag-toolbar";
      this.toolbar.innerHTML = `<div class="ag-summary"><span class="ag-count">0</span><span>张图片<span class="ag-loaded"> · 已加载</span></span></div><div class="ag-tools">${button("select-all", "SquareCheck", "选择已加载图片")}${button("filters", "SlidersHorizontal", "筛选图片", 'aria-expanded="false"')}${button("fit", "Scan", "显示完整图片", `aria-pressed="${this.fit === "contain"}"`)}<label class="ag-size" title="缩略图大小">${icon("Grid2X2")}<input aria-label="缩略图大小" type="range" min="120" max="260" step="10" value="${this.size}"></label></div>`;
      this.filterForm.before(this.toolbar);
      if (this.showOwner) {
        const heading = this.panel.querySelector(".section-title");
        this.toolbar.querySelector(".ag-summary").append(heading.querySelector(".top-actions"));
        heading.hidden = true;
      }
      this.filterForm.hidden = true;
      this.filterForm.classList.add("ag-filters");
      this.filterForm.addEventListener("submit", () => this.updateFilterIndicator());
      this.filterForm.addEventListener("reset", () => setTimeout(() => this.updateFilterIndicator(), 0));
      this.toolbar.addEventListener("click", (event) => this.toolbarAction(event));
      this.toolbar.querySelector("input").addEventListener("input", (event) => { this.size = Number(event.target.value); this.layout(true); this.saveView(); });
      this.container.addEventListener("click", (event) => this.galleryAction(event));
      this.createContextMenu();
      this.container.addEventListener("error", (event) => {
        if (event.target.tagName === "IMG") event.target.closest(".ag-photo")?.classList.add("ag-broken");
      }, true);
      this.container.addEventListener("load", (event) => {
        if (event.target.tagName === "IMG") event.target.closest(".ag-photo")?.classList.add("ag-loaded-photo");
      }, true);
      const select = document.createElement("select");
      select.className = "ag-copy-format";
      select.setAttribute("aria-label", "批量复制格式");
      select.innerHTML = formats;
      select.value = this.format;
      select.addEventListener("change", () => { this.format = select.value; this.saveView(); });
      this.selectionBar.querySelector("div").prepend(select);
      this.createViewer();
      this.layout();
    }

    saveView() { try { localStorage.setItem("zhuapic_album_view", JSON.stringify({ size: this.size, fit: this.fit, format: this.format })); } catch {} }

    layout(animate = false) {
      const tiles = [...this.container.querySelectorAll(".ag-tile")];
      const before = animate ? tiles.map((el) => el.getBoundingClientRect()) : [];
      this.container.style.setProperty("--ag-size", `${this.size}px`);
      this.container.dataset.fit = this.fit;
      if (animate && document.documentElement.dataset.motion !== "reduced" && !matchMedia("(prefers-reduced-motion: reduce)").matches) tiles.forEach((el, i) => {
        const after = el.getBoundingClientRect();
        if (!after.width || !before[i].width) return;
        el.animate([{ transform: `translate(${before[i].x - after.x}px,${before[i].y - after.y}px) scale(${before[i].width / after.width})` }, { transform: "none" }], { duration: 180, easing: "ease-out" });
      });
    }

    setImages(images) {
      this.closeContextMenu();
      const currentID = this.images[this.index]?.id;
      this.images = images;
      this.lastSelected = null;
      const available = new Set(images.map((im) => Number(im.id)));
      for (const id of this.selected) if (!available.has(id)) this.selected.delete(id);
      this.toolbar.querySelector(".ag-count").textContent = images.length;
      const groups = new Map();
      images.forEach((im) => {
        const day = im.date || String(im.created_at || "").slice(0, 10) || "未知日期";
        if (!groups.has(day)) groups.set(day, []);
        groups.get(day).push(im);
      });
      this.container.innerHTML = images.length ? [...groups].map(([day, list]) => {
        const date = new Date(`${day}T12:00:00`);
        const valid = !Number.isNaN(date.getTime());
        const heading = valid ? date.toLocaleDateString("zh-CN", { month: "long", day: "numeric" }) : day;
        const sub = valid ? `${date.getFullYear()} · ${date.toLocaleDateString("zh-CN", { weekday: "long" })}` : "";
        return `<section class="ag-day"><header class="ag-day-head"><div><h3>${escape(heading)}</h3><span>${escape(sub)}</span></div><button class="ag-day-select" type="button" data-ag-day="${escape(day)}" aria-label="选择 ${escape(day)} 的图片">${list.length} 张 ${icon("Check")}</button></header><div class="ag-wall">${list.map((im) => {
          const name = im.original_name || im.public_name || "未命名图片";
          const thumb = safeURL(im.thumb_medium || im.thumb_small || im.url);
          return `<article class="ag-tile" data-ag-id="${escape(im.id)}"><div class="ag-photo"><button type="button" class="ag-open" data-ag-open="${escape(im.id)}" aria-label="查看 ${escape(name)}"><span class="ag-placeholder">${icon("Image")}</span><img src="${escape(thumb)}" alt="${escape(name)}" loading="lazy" decoding="async"></button><button type="button" class="ag-select" data-ag-select="${escape(im.id)}" aria-label="选择 ${escape(name)}" aria-pressed="false">${icon("Check")}</button>${im.is_animated ? '<span class="ag-motion">动图</span>' : ""}<button type="button" class="ag-more" data-ag-more="${escape(im.id)}" title="更多操作" aria-label="更多操作" aria-haspopup="menu">${icon("Ellipsis")}</button><button type="button" class="ag-quick-copy" data-ag-copy="${escape(im.id)}" title="复制图片链接" aria-label="复制图片链接">${icon("Copy")}</button></div><div class="ag-caption"><strong title="${escape(name)}">${escape(name)}</strong><span>${bytes(im.size)}<span>${escape(im.storage_mode || "")}</span></span>${this.showOwner ? `<span class="ag-owner" title="${escape(im.username || (im.user_id ? `用户 #${im.user_id}` : "游客"))}">${icon("UserRound")}<span>${escape(im.username || (im.user_id ? `用户 #${im.user_id}` : "游客"))}</span></span>` : ""}</div></article>`;
        }).join("")}</div></section>`;
      }).join("") : `<div class="ag-empty">${icon("Images")}<h3>还没有图片</h3><p>${this.hasFilters() ? "没有符合条件的图片" : "你的照片，下一刻就在这里"}</p>${this.hasFilters() ? '<button type="button" class="ghost-button compact" data-ag="reset-filters">清除筛选</button>' : '<a class="ghost-button compact" href="/">上传图片</a>'}</div>`;
      this.syncSelection();
      if (this.dialog.open) {
        const next = images.findIndex((im) => im.id === currentID);
        if (next < 0) this.dialog.close();
        else { this.index = next; this.showImage(); }
      }
    }

    hasFilters() { return [...this.filterForm.querySelectorAll("input, select")].some((el) => el.value); }
    updateFilterIndicator() { this.toolbar.querySelector('[data-ag="filters"]').classList.toggle("ag-active", this.hasFilters()); }

    toolbarAction(event) {
      const action = event.target.closest("[data-ag]")?.dataset.ag;
      if (action === "filters") {
        this.filterForm.hidden = !this.filterForm.hidden;
        event.target.closest("button").setAttribute("aria-expanded", String(!this.filterForm.hidden));
        if (!this.filterForm.hidden) this.filterForm.querySelector("input").focus();
      }
      if (action === "fit") {
        this.fit = this.fit === "cover" ? "contain" : "cover";
        event.target.closest("button").setAttribute("aria-pressed", String(this.fit === "contain"));
        this.layout(); this.saveView();
      }
      if (action === "select-all") this.selectList(this.images);
    }

    selectList(list) {
      const all = list.every((im) => this.selected.has(Number(im.id)));
      list.forEach((im) => all ? this.selected.delete(Number(im.id)) : this.selected.add(Number(im.id)));
      this.onSelection(); this.syncSelection();
    }

    syncSelection() {
      this.container.classList.toggle("ag-selecting", this.selected.size > 0);
      this.container.querySelectorAll("[data-ag-select]").forEach((el) => {
        const active = this.selected.has(Number(el.dataset.agSelect));
        el.setAttribute("aria-pressed", String(active));
        el.closest(".ag-tile").classList.toggle("ag-selected", active);
      });
      this.container.querySelectorAll("[data-ag-day]").forEach((el) => {
        const list = this.images.filter((im) => (im.date || String(im.created_at || "").slice(0, 10) || "未知日期") === el.dataset.agDay);
        el.setAttribute("aria-pressed", String(list.every((im) => this.selected.has(Number(im.id)))));
      });
      this.toolbar.querySelector('[data-ag="select-all"]').setAttribute("aria-pressed", String(this.images.length > 0 && this.images.every((im) => this.selected.has(Number(im.id)))));
    }

    galleryAction(event) {
      const btn = event.target.closest("button");
      if (!btn) return;
      if (btn.dataset.agMore) {
        const rect = btn.getBoundingClientRect();
        this.openContextMenu(Number(btn.dataset.agMore), rect.right, rect.bottom + 6, btn);
        return;
      }
      if (btn.dataset.ag === "reset-filters") { this.filterForm.reset(); this.filterForm.requestSubmit(); return; }
      if (btn.dataset.agDay) { this.selectList(this.images.filter((im) => (im.date || String(im.created_at || "").slice(0, 10) || "未知日期") === btn.dataset.agDay)); return; }
      if (btn.dataset.agSelect) {
        const id = Number(btn.dataset.agSelect);
        const index = this.images.findIndex((im) => Number(im.id) === id);
        if (event.shiftKey && this.lastSelected !== null) {
          this.images.slice(Math.min(index, this.lastSelected), Math.max(index, this.lastSelected) + 1).forEach((im) => this.selected.add(Number(im.id)));
        } else if (this.selected.has(id)) this.selected.delete(id); else this.selected.add(id);
        this.lastSelected = index; this.onSelection(); this.syncSelection();
      }
      if (btn.dataset.agCopy) this.copy([this.images.find((im) => Number(im.id) === Number(btn.dataset.agCopy))], "url");
      if (btn.dataset.agOpen) this.open(Number(btn.dataset.agOpen));
    }

    async copy(images, format = this.format) {
      if (!images.length || !images[0]) return;
      try {
        await this.writeClipboard(images.map((im) => code(im, format)).join("\n"));
        this.announce(`已复制 ${images.length > 1 ? images.length + " 张图片的 " : ""}${format === "url" ? "链接" : format + " 代码"}`);
      } catch { this.announce("复制失败，请在详情中选择链接手动复制", true); }
    }
    copySelected() { return this.copy(this.images.filter((im) => this.selected.has(Number(im.id)))); }

    createContextMenu() {
      this.menu = document.createElement("div");
      this.menu.className = "ag-context";
      this.menu.hidden = true;
      this.menu.setAttribute("role", "menu");
      this.menu.setAttribute("aria-label", "图片操作");
      document.body.append(this.menu);
      this.container.addEventListener("contextmenu", (event) => {
        const tile = event.target.closest("[data-ag-id]");
        if (!tile) return;
        event.preventDefault();
        this.openContextMenu(Number(tile.dataset.agId), event.clientX, event.clientY, tile.querySelector(".ag-open"));
      });
      this.container.addEventListener("keydown", (event) => {
        if (event.key !== "ContextMenu" && !(event.shiftKey && event.key === "F10")) return;
        const tile = event.target.closest("[data-ag-id]");
        if (!tile) return;
        event.preventDefault();
        const rect = tile.getBoundingClientRect();
        this.openContextMenu(Number(tile.dataset.agId), rect.left + 20, rect.top + 20, event.target);
      });
      this.menu.addEventListener("click", (event) => {
        const action = event.target.closest("[data-context]")?.dataset.context;
        if (!action) return;
        const im = this.images.find((item) => Number(item.id) === this.contextID);
        this.closeContextMenu(true);
        if (!im) return;
        if (action === "view" || action === "details") {
          this.open(Number(im.id));
          if (action === "details") {
            this.dialog.querySelector(".ag-details").hidden = false;
            this.dialog.querySelector('[data-ag="info"]').setAttribute("aria-expanded", "true");
          }
        } else if (action === "select") {
          const id = Number(im.id);
          this.selected.has(id) ? this.selected.delete(id) : this.selected.add(id);
          this.onSelection(); this.syncSelection();
        } else if (action === "delete") this.onDelete(im.id);
        else this.copy([im], action);
      });
      this.menu.addEventListener("keydown", (event) => {
        const items = [...this.menu.querySelectorAll('[role="menuitem"]')];
        let index = items.indexOf(document.activeElement);
        if (event.key === "Escape" || event.key === "Tab") { event.preventDefault(); this.closeContextMenu(true); return; }
        if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
        event.preventDefault();
        index = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
        items[index].focus();
      });
      document.addEventListener("pointerdown", (event) => { if (!this.menu.contains(event.target)) this.closeContextMenu(); });
      window.addEventListener("resize", () => this.closeContextMenu());
      document.addEventListener("scroll", (event) => { if (!this.menu.contains(event.target)) this.closeContextMenu(); }, true);
    }

    openContextMenu(id, x, y, trigger) {
      const im = this.images.find((item) => Number(item.id) === id);
      if (!im) return;
      this.contextID = id; this.contextTrigger = trigger;
      const item = (action, glyph, label) => `<button type="button" role="menuitem" data-context="${action}">${icon(glyph)}<span>${label}</span></button>`;
      this.menu.innerHTML = `<div class="ag-context-name">${escape(im.original_name || im.public_name)}</div>${item("view", "Scan", "查看图片")}${item("details", "Info", "图片详情")}<hr>${item("url", "Link", "复制链接")}${item("markdown", "Code", "复制 Markdown")}${item("html", "Code", "复制 HTML")}${item("bbcode", "Code", "复制 BBCode")}<hr>${item("select", "SquareCheck", this.selected.has(id) ? "取消选择" : "选择图片")}${item("delete", "Trash2", "移至回收站")}`;
      this.menu.hidden = false;
      this.menu.style.left = `${Math.max(8, Math.min(x, innerWidth - this.menu.offsetWidth - 8))}px`;
      this.menu.style.top = `${Math.max(8, Math.min(y, innerHeight - this.menu.offsetHeight - 8))}px`;
      this.menu.querySelector("button").focus({ preventScroll: true });
    }

    closeContextMenu(restore = false) {
      if (!this.menu || this.menu.hidden) return;
      this.menu.hidden = true;
      if (restore) this.contextTrigger?.focus({ preventScroll: true });
    }

    announce(message, failed = false) {
      this.notice.textContent = message;
      this.notice.classList.toggle("ag-notice-error", failed);
      this.notice.hidden = false;
      clearTimeout(this.noticeTimer);
      this.noticeTimer = setTimeout(() => { this.notice.hidden = true; }, 3500);
    }

    createViewer() {
      this.dialog = document.createElement("dialog");
      this.dialog.className = "ag-viewer";
      this.dialog.setAttribute("aria-label", "图片查看器");
      this.dialog.innerHTML = `<header class="ag-viewer-head"><div class="ag-viewer-title"><strong></strong><span></span></div><div class="ag-viewer-actions">${button("info", "Info", "图片详情", 'aria-expanded="false"')}<a class="ag-icon ag-original" target="_blank" rel="noopener noreferrer" title="打开原图" aria-label="打开原图">${icon("ExternalLink")}</a>${button("close", "X", "关闭查看器")}</div></header><div class="ag-viewer-body"><div class="ag-stage">${button("prev", "ChevronLeft", "上一张")}<img class="ag-view-image" alt="" draggable="false"><span class="ag-image-error" hidden>原图加载失败，请重试或打开原图</span>${button("next", "ChevronRight", "下一张")}</div><aside class="ag-details" hidden><h3>图片详情</h3><dl></dl><label>图片链接<input class="ag-url" readonly aria-label="图片链接"></label>${button("delete", "Trash2", "移至回收站")}</aside></div><footer class="ag-viewer-foot"><div class="ag-zoom-controls">${button("reset", "Maximize", "适应窗口")}<input class="ag-zoom" aria-label="图片缩放" type="range" min="25" max="400" value="100"><output>100%</output></div><div class="ag-viewer-copy"><select aria-label="复制格式">${formats}</select>${button("copy", "Copy", "复制图片链接或代码")}</div></footer>`;
      document.body.append(this.dialog);
      this.notice = document.createElement("div");
      this.notice.className = "ag-notice"; this.notice.hidden = true;
      this.notice.setAttribute("role", "status");
      document.body.append(this.notice);
      this.viewImage = this.dialog.querySelector(".ag-view-image");
      this.stage = this.dialog.querySelector(".ag-stage");
      this.zoomInput = this.dialog.querySelector(".ag-zoom");
      this.dialog.addEventListener("click", (event) => {
        const action = event.target.closest("[data-ag]")?.dataset.ag;
        if (action === "close") this.dialog.close();
        if (action === "prev") this.navigate(-1);
        if (action === "next") this.navigate(1);
        if (action === "reset") this.reset();
        if (action === "copy") this.copy([this.images[this.index]], this.dialog.querySelector("select").value);
        if (action === "delete" && !this.deleting) {
          this.deleting = true;
          const trigger = event.target.closest("button");
          trigger.disabled = true;
          Promise.resolve(this.onDelete(this.images[this.index].id)).finally(() => { this.deleting = false; trigger.disabled = false; });
        }
        if (action === "info") {
          const details = this.dialog.querySelector(".ag-details");
          details.hidden = !details.hidden;
          event.target.closest("button").setAttribute("aria-expanded", String(!details.hidden));
          this.reset();
        }
      });
      this.dialog.querySelector(".ag-url").addEventListener("click", (event) => event.target.select());
      this.dialog.addEventListener("close", () => {
        this.viewImage.removeAttribute("src"); this.pointers.clear();
        document.body.style.overflow = this.previousOverflow;
        document.body.append(this.notice);
      });
      this.dialog.addEventListener("keydown", (event) => {
        if (event.target.matches("input, select, textarea")) return;
        if (event.key === "ArrowLeft") { event.preventDefault(); this.navigate(-1); }
        if (event.key === "ArrowRight") { event.preventDefault(); this.navigate(1); }
        if (["+", "="].includes(event.key)) { event.preventDefault(); this.setZoom(this.scale * 1.2); }
        if (event.key === "-") { event.preventDefault(); this.setZoom(this.scale / 1.2); }
        if (event.key === "0") this.reset();
      });
      this.zoomInput.addEventListener("input", () => this.setZoom(Number(this.zoomInput.value) / 100));
      this.viewImage.addEventListener("error", () => { this.dialog.querySelector(".ag-image-error").hidden = false; });
      this.viewImage.addEventListener("load", () => { this.dialog.querySelector(".ag-image-error").hidden = true; });
      this.dialog.querySelector("select").addEventListener("change", (event) => {
        this.format = event.target.value;
        this.selectionBar.querySelector(".ag-copy-format").value = this.format;
        this.saveView();
      });
      this.stage.addEventListener("wheel", (event) => { event.preventDefault(); this.setZoom(this.scale * Math.exp(-event.deltaY * .002), event.clientX, event.clientY); }, { passive: false });
      this.stage.addEventListener("dblclick", (event) => { if (!event.target.closest("button")) this.scale > 1 ? this.reset() : this.setZoom(2, event.clientX, event.clientY); });
      this.stage.addEventListener("pointerdown", (event) => {
        if (event.target.closest("button") || event.button > 0) return;
        this.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
        this.stage.setPointerCapture(event.pointerId);
        this.swipeStart = { x: event.clientX, y: event.clientY };
      });
      this.stage.addEventListener("pointermove", (event) => {
        const old = this.pointers.get(event.pointerId);
        if (!old) return;
        const other = [...this.pointers.entries()].find(([id]) => id !== event.pointerId)?.[1];
        if (other) {
          const before = Math.hypot(old.x - other.x, old.y - other.y);
          const after = Math.hypot(event.clientX - other.x, event.clientY - other.y);
          if (before > 5) this.setZoom(this.scale * after / before);
          this.swipeStart = null;
        } else if (this.scale > 1) {
          this.x += event.clientX - old.x; this.y += event.clientY - old.y; this.transform();
        }
        this.pointers.set(event.pointerId, { x: event.clientX, y: event.clientY });
      });
      const release = (event) => {
        if (event.type === "pointerup" && event.pointerType === "touch" && this.scale === 1 && this.swipeStart && this.pointers.size === 1) {
          const dx = event.clientX - this.swipeStart.x, dy = event.clientY - this.swipeStart.y;
          if (Math.abs(dx) > 70 && Math.abs(dx) > Math.abs(dy) * 1.5) this.navigate(dx < 0 ? 1 : -1);
        }
        this.pointers.delete(event.pointerId); this.swipeStart = null;
      };
      this.stage.addEventListener("pointerup", release);
      this.stage.addEventListener("pointercancel", release);
      this.stage.addEventListener("lostpointercapture", release);
    }

    open(id) {
      this.index = this.images.findIndex((im) => Number(im.id) === id);
      if (this.index < 0) return;
      this.previousOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      this.dialog.append(this.notice);
      this.dialog.showModal(); this.showImage();
      this.dialog.querySelector('[data-ag="close"]').focus();
    }
    navigate(delta) { const next = this.index + delta; if (next >= 0 && next < this.images.length) { this.index = next; this.showImage(); } }
    showImage() {
      const im = this.images[this.index];
      if (!im) return;
      this.reset();
      this.dialog.querySelector(".ag-image-error").hidden = true;
      this.viewImage.src = imageURL(im);
      this.viewImage.alt = im.original_name || im.public_name || "图片";
      this.dialog.querySelector(".ag-viewer-title strong").textContent = this.viewImage.alt;
      this.dialog.querySelector(".ag-viewer-title span").textContent = `${this.index + 1} / ${this.images.length} · ${bytes(im.size)}`;
      this.dialog.querySelector('[data-ag="prev"]').disabled = this.index === 0;
      this.dialog.querySelector('[data-ag="next"]').disabled = this.index === this.images.length - 1;
      this.dialog.querySelector(".ag-original").href = imageURL(im);
      this.dialog.querySelector(".ag-url").value = imageURL(im);
      this.dialog.querySelector("select").value = this.format;
      const rows = [["文件名", this.viewImage.alt], ["尺寸", im.width && im.height ? `${im.width} × ${im.height}` : "未知"], ["格式", `${im.mime_type || "未知"}${im.is_animated ? " · 动图" : ""}`], ["大小", bytes(im.size)], ["存储", ({ local: "Local", s3: "S3", mirror: "Mirror · 双副本" })[im.storage_mode] || im.storage_mode], ["上传时间", im.created_at ? new Date(im.created_at).toLocaleString("zh-CN", { hour12: false }) : "未知"]];
      if (im.username) rows.push(["上传者", im.username]);
      if (im.expires_at) rows.push(["过期时间", new Date(im.expires_at).toLocaleString("zh-CN")]);
      this.dialog.querySelector("dl").innerHTML = rows.map(([label, value]) => `<div><dt>${escape(label)}</dt><dd>${escape(value)}</dd></div>`).join("");
    }
    reset() { this.scale = 1; this.x = 0; this.y = 0; this.transform(); }
    setZoom(next, clientX, clientY) {
      const scale = Math.max(.25, Math.min(4, next));
      const rect = this.stage.getBoundingClientRect();
      const px = clientX === undefined ? 0 : clientX - rect.left - rect.width / 2;
      const py = clientY === undefined ? 0 : clientY - rect.top - rect.height / 2;
      this.x = px - (px - this.x) * scale / this.scale;
      this.y = py - (py - this.y) * scale / this.scale;
      this.scale = scale;
      if (scale <= 1) { this.x = 0; this.y = 0; }
      this.transform();
    }
    transform() {
      if (!this.viewImage) return;
      const maxX = Math.max(0, (this.viewImage.offsetWidth * this.scale - this.stage.clientWidth) / 2 + 64);
      const maxY = Math.max(0, (this.viewImage.offsetHeight * this.scale - this.stage.clientHeight) / 2 + 64);
      this.x = Math.max(-maxX, Math.min(maxX, this.x)); this.y = Math.max(-maxY, Math.min(maxY, this.y));
      this.viewImage.style.transform = `translate(${this.x}px,${this.y}px) scale(${this.scale})`;
      this.zoomInput.value = Math.round(this.scale * 100);
      this.dialog.querySelector("output").textContent = `${Math.round(this.scale * 100)}%`;
      this.stage.classList.toggle("ag-zoomed", this.scale > 1);
    }
  }
  window.ZhuaAlbum = ZhuaAlbum;
})();
