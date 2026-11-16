(() => {
  function setup(containerID, toolbarID, preferenceKey, defaultView = "cards") {
    const container = document.getElementById(containerID), toolbar = document.getElementById(toolbarID);
    const search = toolbar.querySelector("[data-catalog-search]"), filter = toolbar.querySelector("[data-catalog-filter]");
    let chosen;
    try { chosen = localStorage.getItem(preferenceKey); } catch {}
    if (!["cards", "list", "compact"].includes(chosen)) chosen = null;
    const icons = { cards: ["Grid2X2", "卡片"], list: ["ListTodo", "列表"], compact: ["PanelTop", "紧凑"] };
    const switcher = toolbar.querySelector("[data-view-switch]");
    for (const [view, [name, label]] of Object.entries(icons)) {
      const button = document.createElement("button"); button.type = "button"; button.className = "icon-button";
      button.dataset.view = view; button.title = label; button.setAttribute("aria-label", label);
      const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
      for (const [key,value] of Object.entries({ viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "1.7", "aria-hidden": "true" })) svg.setAttribute(key,value);
      for (const [tag,attrs] of window.AlbumIcons[name]) { const node=document.createElementNS(svg.namespaceURI,tag); for (const [k,v] of Object.entries(attrs)) node.setAttribute(k,v); svg.append(node); }
      button.append(svg); switcher.append(button);
      button.addEventListener("click", () => { chosen=view; try { localStorage.setItem(preferenceKey,view); } catch {} refresh(); });
    }
    function refresh() {
      const rows = [...container.children].filter(el => el.hasAttribute("data-catalog-row"));
      const view = chosen || (rows.length > 6 ? "list" : defaultView);
      container.dataset.view = view;
      switcher.querySelectorAll("button").forEach(button => button.setAttribute("aria-pressed",String(button.dataset.view === view)));
      const query = search.value.trim().toLocaleLowerCase();
      let visible = 0;
      for (const row of rows) {
        row.hidden = !(row.dataset.search || "").toLocaleLowerCase().includes(query) || Boolean(filter.value && !(row.dataset.filter || "").split(" ").includes(filter.value));
        if (!row.hidden) visible++;
      }
      toolbar.querySelector("[data-catalog-count]").textContent = visible + " / " + rows.length;
      document.getElementById(containerID + "Empty").hidden = visible > 0;
    }
    search.addEventListener("input",refresh); filter.addEventListener("change",refresh);
    return { refresh };
  }
  window.OrimgViews = { setup };
})();
