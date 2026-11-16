(() => {
  const key = "zhuapic_appearance";
  const colors = {
    lime: ["青柠", "#caea72", "#456c25", "#cee98c"],
    blue: ["蓝色", "#3985ee", "#205eba", "#a8caff"],
    purple: ["紫色", "#a16ad3", "#7942a8", "#d9b4f5"],
    pink: ["粉色", "#e776a3", "#aa3866", "#f5b2cd"],
    red: ["红色", "#e55c60", "#b3333b", "#ffb3b5"],
    orange: ["橙色", "#ee9140", "#985212", "#f5c095"],
    yellow: ["黄色", "#e9bf36", "#7f6514", "#ecdb8b"],
    green: ["绿色", "#66ac73", "#30733f", "#a5d7ae"],
    graphite: ["石墨", "#949a96", "#525b55", "#c8cfca"],
  };
  const defaults = { theme: "auto", color: "site", glass: "solid", highlight: "accent", motion: true };
  let siteColor = "lime";
  try { const cached = localStorage.getItem("zhuapic_site_accent"); if (Object.hasOwn(colors, cached)) siteColor = cached; } catch {}
  const system = matchMedia("(prefers-color-scheme: dark)");
  let prefs, dialog, control, glyph;
  const normalize = (value = {}) => ({
    theme: ["auto", "light", "dark"].includes(value.theme) ? value.theme : defaults.theme,
    color: value.color === "site" || Object.hasOwn(colors, value.color) ? value.color : defaults.color,
    glass: ["clear", "solid"].includes(value.glass) ? value.glass : defaults.glass,
    highlight: value.highlight === "accent" || Object.hasOwn(colors, value.highlight) ? value.highlight : defaults.highlight,
    motion: value.motion !== false,
  });
  const read = () => {
    try {
      const saved = localStorage.getItem(key);
      return normalize(saved ? JSON.parse(saved) : { theme: localStorage.getItem("zhuapic_theme") });
    } catch { return { ...defaults }; }
  };
  function apply() {
    const root = document.documentElement;
    const dark = prefs.theme === "dark" || (prefs.theme === "auto" && system.matches);
    if (prefs.theme === "auto") delete root.dataset.theme; else root.dataset.theme = prefs.theme;
    root.dataset.glass = prefs.glass;
    root.dataset.motion = prefs.motion ? "system" : "reduced";
    const colorName = prefs.color === "site" ? siteColor : prefs.color;
    root.dataset.accent = colorName;
    const color = colors[colorName];
    root.style.setProperty("--ui-accent", color[dark ? 3 : 2]);
    root.style.setProperty("--accent", color[1]);
    root.style.setProperty("--primary", color[dark ? 3 : 2]);
    root.style.setProperty("--on-primary", dark ? "#19201b" : "#ffffff");
    root.style.setProperty("--highlight-color", colors[prefs.highlight === "accent" ? colorName : prefs.highlight][1]);
    root.style.colorScheme = dark ? "dark" : "light";
    if (control) {
      control.replaceChildren(glyph(({ auto: "Monitor", light: "Sun", dark: "Moon" })[prefs.theme]));
      control.title = "外观设置"; control.setAttribute("aria-label", "外观设置");
    }
    if (dialog) {
      dialog.querySelectorAll('input[type="radio"]').forEach(input => { input.checked = prefs[input.name] === input.value; });
      dialog.querySelector('[name="highlight"]').value = prefs.highlight;
      dialog.querySelector('[name="motion"]').checked = prefs.motion;
      dialog.querySelector(".appearance-color-name").textContent = `${prefs.color === "site" ? "跟随站点 · " : ""}${color[0]}`;
      dialog.querySelector('[name="followSite"]').checked = prefs.color === "site";
    }
  }
  function save() {
    try { localStorage.setItem(key, JSON.stringify(prefs)); localStorage.setItem("zhuapic_theme", prefs.theme); } catch {}
    apply();
  }
  function mount(button, icon) {
    control = button; glyph = icon;
    control.setAttribute("aria-haspopup", "dialog");
    control.setAttribute("aria-controls", "appearanceDialog");
    dialog = document.createElement("dialog");
    dialog.id = "appearanceDialog"; dialog.className = "appearance-dialog";
    dialog.setAttribute("aria-labelledby", "appearanceTitle");
    const option = (name, value, label, preview) => `<label class="appearance-choice"><input type="radio" name="${name}" value="${value}"><span class="appearance-preview ${preview}" aria-hidden="true"><i></i><b></b><em></em></span><span>${label}</span></label>`;
    dialog.innerHTML = `<form class="appearance-form"><header><div><h2 id="appearanceTitle">外观</h2></div><button class="icon-button" type="button" data-appearance-close aria-label="关闭外观设置"></button></header>
      <fieldset><legend>显示模式</legend><div class="appearance-modes">${option("theme", "auto", "自动", "preview-auto")}${option("theme", "light", "浅色", "preview-light")}${option("theme", "dark", "深色", "preview-dark")}</div></fieldset>
      <fieldset><legend>面板效果</legend><div class="appearance-surfaces">${option("glass", "clear", "通透", "preview-clear")}${option("glass", "solid", "实色", "preview-solid")}</div></fieldset>
      <fieldset><legend>强调色 <span class="appearance-color-name"></span></legend><div class="appearance-swatches">${Object.entries(colors).map(([value, [label, swatch]]) => `<label class="appearance-swatch" title="${label}" style="--swatch:${swatch}"><input type="radio" name="color" value="${value}" aria-label="${label}"><span aria-hidden="true"></span></label>`).join("")}</div></fieldset>
      <div class="appearance-row"><label for="appearanceHighlight">文本选中颜色</label><select name="highlight" id="appearanceHighlight"><option value="accent">跟随强调色</option>${Object.entries(colors).map(([value, [label]]) => `<option value="${value}">${label}</option>`).join("")}</select></div>
      <div class="appearance-row"><label for="appearanceFollowSite">强调色跟随站点</label><input type="checkbox" role="switch" name="followSite" id="appearanceFollowSite"></div>
      <div class="appearance-row"><label for="appearanceMotion">界面动效</label><input type="checkbox" role="switch" name="motion" id="appearanceMotion"></div>
      <footer><button type="button" class="ghost-button" data-appearance-reset>恢复默认</button><button type="button" class="primary-button" data-appearance-close>完成</button></footer></form>`;
    dialog.querySelector("[data-appearance-close]").append(glyph("X"));
    document.body.append(dialog);
    control.addEventListener("click", () => {
      apply(); dialog.showModal();
      dialog.querySelector('input[name="theme"]:checked').focus();
    });
    dialog.addEventListener("change", event => {
      const input = event.target;
      if (input.name === "followSite") { prefs.color = input.checked ? "site" : siteColor; save(); return; }
      if (!Object.hasOwn(defaults, input.name)) return;
      prefs = normalize({ ...prefs, [input.name]: input.type === "checkbox" ? input.checked : input.value }); save();
    });
    dialog.addEventListener("click", event => {
      if (event.target.closest("[data-appearance-close]")) dialog.close();
      if (event.target.closest("[data-appearance-reset]")) { prefs = { ...defaults }; save(); }
      if (event.target === dialog) {
        const rect = dialog.getBoundingClientRect();
        if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) dialog.close();
      }
    });
    dialog.querySelector("form").addEventListener("submit", event => event.preventDefault());
    apply();
  }
  prefs = read(); apply();
  system.addEventListener("change", apply);
  function setSiteDefault(value) {
    siteColor = Object.hasOwn(colors, value) ? value : "lime";
    try { localStorage.setItem("zhuapic_site_accent", siteColor); } catch {}
    document.querySelectorAll('[name="siteAccentChoice"]').forEach(input => { input.checked = input.value === siteColor; });
    apply();
  }
  function mountSiteColors() {
    const select = document.querySelector('[name="default_accent_color"]');
    if (!select) return;
    select.hidden = true;
    const group = document.createElement("div");
    group.className = "appearance-swatches site-accent-swatches";
    group.setAttribute("role", "radiogroup"); group.setAttribute("aria-label", "默认强调色");
    Object.entries(colors).forEach(([value, [label, swatch]]) => {
      const choice = document.createElement("label"); choice.className = "appearance-swatch"; choice.title = label;
      choice.style.setProperty("--swatch", swatch);
      const input = document.createElement("input"); input.type = "radio"; input.name = "siteAccentChoice"; input.value = value; input.setAttribute("aria-label", label);
      input.checked = value === (select.value || siteColor);
      input.addEventListener("change", () => { if (input.checked) select.value = value; });
      const sample = document.createElement("span"); sample.setAttribute("aria-hidden", "true");
      choice.append(input, sample); group.append(choice);
    });
    // Keep radio labels outside the select's enclosing label.
    select.closest("label").after(group);
  }
  document.addEventListener("DOMContentLoaded", mountSiteColors);
  window.addEventListener("storage", event => {
    if (event.key === "zhuapic_site_accent") { siteColor = Object.hasOwn(colors, event.newValue) ? event.newValue : "lime"; apply(); }
    if (event.key === key || event.key === null) { prefs = read(); apply(); }
  });
  window.ZhuaAppearance = { mount, setSiteDefault };
})();
