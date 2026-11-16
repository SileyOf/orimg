(() => {
  function text(settings, key, fallback = "") {
    return typeof settings[key] === "string" ? settings[key] : fallback;
  }
  function setText(el, value) {
    if (!el) return;
    el.textContent = value;
    el.hidden = !value.trim();
  }
  function apply(settings) {
    const name = text(settings, "site_name");
    document.querySelectorAll("[data-site-name]").forEach(el => setText(el, name));
    document.querySelectorAll(".brand-logo").forEach(img => {
      img.dataset.logoPending = "";
      img.onload = img.onerror = () => { delete img.dataset.logoPending; };
      img.src = text(settings, "site_logo_url") || "/assets/logo.svg";
    });
    document.querySelectorAll("a.brand").forEach(el => el.setAttribute("aria-label", name || "首页"));
    const pageName = document.body.dataset.pageTitle || (document.body.classList.contains("admin-page") ? "管理后台" : document.body.classList.contains("dashboard-page") ? "用户中心" : "图片上传");
    document.title = name ? (pageName === "图片上传" ? name : `${name} · ${pageName}`) : pageName;
    window.ZhuaAppearance.setSiteDefault(settings.default_accent_color);
    window.OrimgBackground.apply(settings);
    delete document.body.dataset.brandPending;
  }
  async function load() {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 12000);
    try {
      const response = await fetch("/api/settings/public", { cache: "no-store", signal: controller.signal });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const settings = await response.json();
      if (!settings || Array.isArray(settings) || typeof settings !== "object") throw new Error("Invalid site settings");
      apply(settings);
      return settings;
    } finally { clearTimeout(timeout); }
  }
  window.ZhuaBranding = { text, setText, apply, load };
})();
