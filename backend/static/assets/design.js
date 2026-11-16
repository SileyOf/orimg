(() => {
  // Lucide Mail, ISC license, matching the bundled icon nodes.
  window.AlbumIcons.Mail = [["path", {d:"m22 7-8.991 5.727a2 2 0 0 1-2.009 0L2 7"}], ["rect", {x:"2",y:"4",width:"20",height:"16",rx:"2"}]];
  const markURL = "/assets/logo-mark.svg" + new URL(document.currentScript.src).search;
  const glyph = (name) => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    Object.entries({ viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "1.7", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" }).forEach(([k, v]) => svg.setAttribute(k, v));
    (window.AlbumIcons[name] || []).forEach(([tag, attrs]) => {
      const el = document.createElementNS(svg.namespaceURI, tag);
      Object.entries(attrs).forEach(([k, v]) => el.setAttribute(k, v));
      svg.append(el);
    });
    return svg;
  };
  const navIcons = { overview: "LayoutDashboard", storage: "HardDrive", tasks: "ListTodo", mail: "Mail", site: "PanelTop", security: "ShieldCheck", audit: "ScrollText", groups: "Users", users: "UserRound", images: "Images", trash: "Trash2", upload: "Upload", photos: "Images", profile: "Settings" };
  window.OrimgGlyph = glyph;
  document.querySelectorAll(".admin-nav button, .dashboard-nav button").forEach((button) => {
    const label = document.createElement("span"); label.textContent = button.textContent;
    button.replaceChildren(glyph(navIcons[button.dataset.panel || button.dataset.page]), label);
  });
  document.querySelectorAll(".capture-icon").forEach((el) => {
    const mark = document.createElement("img"); mark.src = "/assets/logo.svg"; mark.alt = "";
    el.className = "upload-emblem"; el.replaceChildren(mark);
  });
  const isPublic = document.body.classList.contains("mode-classic");
  let control = document.getElementById("themeButton");
  if (!control) {
    control = document.createElement("button"); control.type = "button"; control.className = "icon-button";
    control.id = "appearanceButton";
    document.querySelector(".dashboard-topbar .top-actions, .admin-toolbar .top-actions")?.prepend(control);
  }
  window.ZhuaAppearance.mount(control, glyph);
  const tintLogo = (img) => {
    let url;
    try { url = new URL(img.getAttribute("src"), location.href); } catch { return; }
    const builtIn = url.origin === location.origin && ["/assets/logo.svg", "/assets/logo-mark.svg"].includes(url.pathname);
    img.classList.toggle("appearance-logo", builtIn);
    if (builtIn && img.getAttribute("src") !== markURL) img.src = markURL;
  };
  document.querySelectorAll("img").forEach(tintLogo);
  new MutationObserver((records) => records.forEach((record) => {
    if (record.type === "attributes") tintLogo(record.target);
    else record.addedNodes.forEach((node) => {
      if (node.nodeType !== 1) return;
      if (node.matches("img")) tintLogo(node);
      node.querySelectorAll("img").forEach(tintLogo);
    });
  })).observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ["src"] });

  // Native popovers escape the stacking contexts created by upload progress rows.
  let openCopyMenu;
  const closeCopyMenu = () => { if (openCopyMenu) openCopyMenu.open = false; };
  document.addEventListener("toggle", (event) => {
    const details = event.target;
    if (!details.matches?.("details.copy-menu")) return;
    const menu = details.querySelector("div");
    if (!details.open) {
      if (menu.matches(":popover-open")) menu.hidePopover();
      if (openCopyMenu === details) openCopyMenu = null;
      return;
    }
    if (openCopyMenu && openCopyMenu !== details) openCopyMenu.open = false;
    openCopyMenu = details;
    if (menu.showPopover) {
      if (!menu.hasAttribute("popover")) {
        menu.setAttribute("popover", "auto");
        menu.addEventListener("toggle", (e) => { if (e.newState === "closed" && !menu.matches(":popover-open")) details.open = false; });
      }
      menu.showPopover();
    }
    const anchor = details.getBoundingClientRect();
    const rect = menu.getBoundingClientRect();
    const top = anchor.bottom + rect.height + 8 <= innerHeight - 8 ? anchor.bottom + 8 : Math.max(8, anchor.top - rect.height - 8);
    menu.style.left = `${Math.max(8, Math.min(anchor.right - rect.width, innerWidth - rect.width - 8))}px`;
    menu.style.top = `${top}px`;
  }, true);
  document.addEventListener("click", (event) => { if (openCopyMenu && !openCopyMenu.contains(event.target)) closeCopyMenu(); });
  document.addEventListener("keydown", (event) => {
    if (!openCopyMenu) return;
    if (event.key === "Escape") { const summary = openCopyMenu.querySelector("summary"); closeCopyMenu(); summary.focus(); }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const buttons = [...openCopyMenu.querySelectorAll("button")];
    const current = buttons.indexOf(document.activeElement);
    const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (current + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
    buttons[next]?.focus();
  });
  window.addEventListener("resize", closeCopyMenu);
  document.addEventListener("scroll", (event) => { if (!openCopyMenu?.contains(event.target)) closeCopyMenu(); }, true);
  if (isPublic) {
    const dropzone = document.getElementById("dropzone");
    const observer = new MutationObserver(() => document.body.classList.toggle("upload-has-files", !document.getElementById("queuePanel").hidden));
    observer.observe(document.getElementById("queuePanel"), { attributes: true, attributeFilter: ["hidden"] });
    dropzone.addEventListener("pointermove", (event) => {
      if (document.documentElement.dataset.motion === "reduced" || matchMedia("(prefers-reduced-motion: reduce)").matches || event.pointerType === "touch") return;
      const rect = dropzone.getBoundingClientRect();
      dropzone.style.setProperty("--tilt-x", `${(event.clientX - rect.left - rect.width / 2) / rect.width * 8}deg`);
      dropzone.style.setProperty("--tilt-y", `${-(event.clientY - rect.top - rect.height / 2) / rect.height * 8}deg`);
    });
    dropzone.addEventListener("pointerleave", () => { dropzone.style.setProperty("--tilt-x", "0deg"); dropzone.style.setProperty("--tilt-y", "0deg"); });
  }
})();
