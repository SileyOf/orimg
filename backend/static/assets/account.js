(() => {
  const glyph = (name) => {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    Object.entries({ viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "1.7", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" }).forEach(([key, value]) => svg.setAttribute(key, value));
    (window.AlbumIcons[name] || []).forEach(([tag, attrs]) => {
      const node = document.createElementNS(svg.namespaceURI, tag);
      Object.entries(attrs).forEach(([key, value]) => node.setAttribute(key, value));
      svg.append(node);
    });
    return svg;
  };
  const localPath = (value, fallback) => {
    try { const url = new URL(value, location.origin); return url.origin === location.origin ? url.pathname + url.search + url.hash : fallback; }
    catch { return fallback; }
  };
  function mount(slot, options) {
    if (!slot || !options.user) return;
    slot._accountCleanup?.();
    slot.replaceChildren();
    const user = options.user;
    const name = user.username || user.email || "我的账号";
    const trigger = document.createElement("button");
    trigger.type = "button"; trigger.className = "account-avatar";
    trigger.setAttribute("aria-label", "账号菜单"); trigger.setAttribute("aria-haspopup", "menu");
    trigger.setAttribute("aria-expanded", "false"); trigger.title = "账号菜单";
    trigger.textContent = Array.from(name.trim())[0]?.toLocaleUpperCase() || "O";
    if (/^\/avatars\/[a-f0-9]{32}\.png$/.test(user.avatar_url || "")) {
      const img = document.createElement("img"); img.src = user.avatar_url; img.alt = "";
      img.addEventListener("error", () => { trigger.textContent = Array.from(name.trim())[0]?.toLocaleUpperCase() || "O"; });
      trigger.replaceChildren(img);
    }
    const menu = document.createElement("div"); menu.className = "account-menu";
    menu.setAttribute("popover", "auto"); menu.setAttribute("role", "menu"); menu.setAttribute("aria-label", "我的账号");
    const identity = document.createElement("div"); identity.className = "account-identity";
    const strong = document.createElement("strong"); strong.textContent = name;
    const email = document.createElement("small"); email.textContent = user.email || "";
    identity.append(strong, email); menu.append(identity);
    const close = () => { if (menu.matches(":popover-open")) menu.hidePopover(); };
    const add = (label, icon, href, action, danger = false) => {
      const entry = document.createElement(href ? "a" : "button");
      if (href) entry.href = href; else entry.type = "button";
      entry.setAttribute("role", "menuitem");
      const text = document.createElement("span"); text.textContent = label;
      entry.append(glyph(icon), text);
      if (danger) entry.className = "account-logout";
      entry.addEventListener("click", () => { close(); action?.(); }); menu.append(entry);
    };
    const userPath = localPath(options.userPath || "/dashboard", "/dashboard");
    if (!options.onSettings) add("个人空间", "LayoutDashboard", userPath);
    add("账号设置", "Settings", options.onSettings ? null : userPath + "#account", options.onSettings);
    if (user.role === "admin" && !options.isAdminPage) add("管理后台", "ShieldCheck", localPath(options.adminPath || "/admin", "/admin"));
    menu.append(document.createElement("hr"));
    add("退出登录", "LogOut", null, () => {
      localStorage.removeItem("zhuapic_token");
      if (options.onLogout) options.onLogout(); else location.reload();
    }, true);
    slot.append(trigger, menu);
    const position = () => {
      const anchor = trigger.getBoundingClientRect(), rect = menu.getBoundingClientRect();
      menu.style.left = `${Math.max(8, Math.min(anchor.right - rect.width, innerWidth - rect.width - 8))}px`;
      menu.style.top = `${Math.max(8, Math.min(anchor.bottom + 10, innerHeight - rect.height - 8))}px`;
    };
    trigger.addEventListener("click", () => {
      if (menu.matches(":popover-open")) close();
      else { menu.showPopover(); position(); }
    });
    trigger.addEventListener("keydown", (event) => {
      if (!["ArrowDown", "ArrowUp"].includes(event.key)) return;
      event.preventDefault();
      if (!menu.matches(":popover-open")) menu.showPopover();
      position();
      const items = menu.querySelectorAll('[role="menuitem"]');
      items[event.key === "ArrowUp" ? items.length - 1 : 0]?.focus();
    });
    menu.addEventListener("toggle", (event) => trigger.setAttribute("aria-expanded", String(event.newState === "open")));
    menu.addEventListener("keydown", (event) => {
      if (event.key === "Escape") { event.preventDefault(); close(); trigger.focus(); return; }
      if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const items = [...menu.querySelectorAll('[role="menuitem"]')];
      const current = items.indexOf(document.activeElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
      items[next]?.focus();
    });
    window.addEventListener("resize", close);
    slot._accountCleanup = () => { close(); window.removeEventListener("resize", close); };
  }
  window.OrimgAccount = { mount };
  window.addEventListener("storage", (event) => {
    if (event.key === "zhuapic_token") location.reload();
  });
})();
