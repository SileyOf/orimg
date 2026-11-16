(() => {
  let layer, generation = 0;
  const clamp = (value, low, high, fallback) => {
    const number = value === "" || value == null ? fallback : Number(value);
    return Number.isFinite(number) ? Math.max(low, Math.min(high, number)) : fallback;
  };
  function safeURL(value) {
    if (!value) return "";
    try {
      const url = new URL(value, location.origin);
      if (url.username || url.password || url.hash || /[\r\n\\]/.test(value)) return "";
      if (url.protocol === "https:" || (value.startsWith("/assets/") && url.origin === location.origin)) return url.href;
    } catch {}
    return "";
  }
  async function apply(settings) {
    const current = ++generation;
    const body = document.body;
    body.style.setProperty("--background-overlay", `${clamp(settings.background_overlay, 75, 95, 82)}%`);
    body.style.setProperty("--background-blur", `${clamp(settings.background_blur, 0, 20, 0)}px`);
    const url = safeURL(settings.background_url);
    if (!url) { delete body.dataset.wallpaper; return !settings.background_url; }
    if (layer?.dataset.url === url) { body.dataset.wallpaper = ""; return true; }
    delete body.dataset.wallpaper;
    const img = new Image(); img.referrerPolicy = "no-referrer";
    const loaded = await new Promise(resolve => {
      const timer = setTimeout(() => resolve(false), 12000);
      img.onload = () => { clearTimeout(timer); resolve(true); };
      img.onerror = () => { clearTimeout(timer); resolve(false); };
      img.src = url;
    });
    if (current !== generation) return false;
    if (!loaded) return false;
    if (!layer) {
      layer = document.createElement("div"); layer.className = "site-wallpaper"; layer.setAttribute("aria-hidden", "true");
      document.body.prepend(layer);
    }
    layer.replaceChildren(img); layer.dataset.url = url;
    body.dataset.wallpaper = "";
    return true;
  }
  function syncEditor() {
    const form = document.getElementById("siteForm");
    if (!form) return;
    document.getElementById("backgroundOverlayValue").value = `${form.elements.background_overlay.value}%`;
    document.getElementById("backgroundBlurValue").value = `${form.elements.background_blur.value}px`;
    const preview = document.getElementById("backgroundPreview");
    const url = safeURL(form.elements.background_url.value);
    preview.hidden = !url;
    document.getElementById("backgroundEmpty").hidden = !!url;
    preview.referrerPolicy = "no-referrer";
    if (url) preview.src = url; else preview.removeAttribute("src");
    preview.onerror = () => { preview.hidden = true; document.getElementById("backgroundEmpty").hidden = false; };
  }
  document.addEventListener("DOMContentLoaded", () => {
    const form = document.getElementById("siteForm");
    if (!form) return;
    const message = document.getElementById("backgroundMessage");
    let previewGeneration = 0;
    const draft = () => Object.fromEntries(["background_url", "background_overlay", "background_blur"].map(key => [key, form.elements[key].value]));
    async function preview() {
      const current = ++previewGeneration;
      syncEditor();
      const ok = await apply(draft());
      if (current !== previewGeneration) return;
      message.textContent = ok ? "预览已更新，保存页面设置后生效" : "无法加载背景，请检查图片地址";
    }
    document.getElementById("previewBackground").addEventListener("click", preview);
    ["background_overlay", "background_blur"].forEach(key => form.elements[key].addEventListener("input", preview));
    document.getElementById("clearBackground").addEventListener("click", () => { form.elements.background_url.value = ""; preview(); });
    document.getElementById("chooseBackground").addEventListener("click", () => document.getElementById("backgroundFile").click());
    document.getElementById("backgroundFile").addEventListener("change", async event => {
      const file = event.target.files[0]; event.target.value = "";
      if (!file) return;
      if (file.size > 8 * 1024 * 1024) { message.textContent = "背景图片不能超过 8MB"; return; }
      const button = document.getElementById("chooseBackground"); button.disabled = true;
      message.textContent = "正在上传背景";
      try {
        const body = new FormData(); body.append("background", file);
        const response = await fetch("/api/admin/background", { method: "POST", headers: { Authorization: `Bearer ${localStorage.getItem("zhuapic_token") || ""}` }, body });
        const result = await response.json();
        if (!response.ok) throw new Error(result.error || "背景上传失败");
        form.elements.background_url.value = result.url;
        await preview();
      } catch (err) { message.textContent = err.message; }
      finally { button.disabled = false; }
    });
  });
  window.OrimgBackground = { apply, syncEditor };
})();
