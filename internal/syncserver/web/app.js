(() => {
  const THEMES = [
    { id: "dark", label: "Win11 Dark" },
    { id: "antigravity", label: "Antigravity IDE" },
    { id: "nord", label: "Nord" },
    { id: "dracula", label: "Dracula" },
    { id: "emerald", label: "Emerald" },
    { id: "light", label: "Light" },
  ];

  const state = {
    activeTab: "overview",
    selectedHostID: "all", // "all" or specific host ID in Fleet Overview / Sidebar
    explorerHostID: "",
    platform: "windows",
    source: "server",
    settings: {
      theme: "dark",
      speedUnit: "bits",
      hideLoopback: true,
      refreshRateMs: 1000,
      retentionDays: 365,
      defaultPollMs: 1000,
      historySyncSec: 60,
    },
    cluster: null,
    hostsList: [],
    todayHostTotals: {}, // hostId -> { rxBytes, txBytes }
    todayFleetRx: 0,
    todayFleetTx: 0,
    historyFilter: {
      period: "",
      days: 14,
      start: "",
      end: "",
      hostId: "all",
    },
    historyReport: null,
    fleetHover: null,
    explorerHover: null,
    histHover: null,
    editingHostID: "",
    lastTodayFetchAt: 0,
  };

  // Core DOM refs
  const htmlEl = document.documentElement;
  const navItems = document.querySelectorAll(".nav-item");
  const tabPanels = document.querySelectorAll(".tab-panel");
  const viewTitleEl = document.getElementById("viewTitle");
  const viewSubtitleEl = document.getElementById("viewSubtitle");
  const hostSidebarList = document.getElementById("hostSidebarList");
  const dataSourceBadge = document.getElementById("dataSourceBadge");
  const quickThemeLabel = document.getElementById("quickThemeLabel");

  // Overview DOM
  const fleetRxSpeedEl = document.getElementById("fleetRxSpeed");
  const fleetTxSpeedEl = document.getElementById("fleetTxSpeed");
  const fleetTodayRxEl = document.getElementById("fleetTodayRx");
  const fleetTodayTxEl = document.getElementById("fleetTodayTx");
  const fleetHostsCountEl = document.getElementById("fleetHostsCount");
  const fleetTodayCombinedEl = document.getElementById("fleetTodayCombined");
  const fleetScaleMaxEl = document.getElementById("fleetScaleMax");
  const fleetCanvas = document.getElementById("fleetRealtimeCanvas");
  const fleetTooltip = document.getElementById("fleetTooltip");
  const hostOverviewGrid = document.getElementById("hostOverviewGrid");

  // Explorer DOM
  const explorerHostSelect = document.getElementById("explorerHostSelect");
  const explorerHostMeta = document.getElementById("explorerHostMeta");
  const explorerQuotaBanner = document.getElementById("explorerQuotaBanner");
  const explorerQuotaCycle = document.getElementById("explorerQuotaCycle");
  const explorerQuotaNumbers = document.getElementById("explorerQuotaNumbers");
  const explorerQuotaFill = document.getElementById("explorerQuotaFill");
  const explorerQuotaProjection = document.getElementById("explorerQuotaProjection");
  const expRxSpeedEl = document.getElementById("expRxSpeed");
  const expTxSpeedEl = document.getElementById("expTxSpeed");
  const expSessRxEl = document.getElementById("expSessRx");
  const expSessTxEl = document.getElementById("expSessTx");
  const expPollLatencyEl = document.getElementById("expPollLatency");
  const expSourceModeEl = document.getElementById("expSourceMode");
  const expNicCountEl = document.getElementById("expNicCount");
  const expPrimaryNicEl = document.getElementById("expPrimaryNic");
  const expPingBadgesEl = document.getElementById("expPingBadges");
  const explorerCanvas = document.getElementById("explorerCanvas");
  const explorerTooltip = document.getElementById("explorerTooltip");
  const expProcTableBody = document.getElementById("expProcTableBody");
  const expSpeedTestBody = document.getElementById("expSpeedTestBody");
  const expSpeedTestStatus = document.getElementById("expSpeedTestStatus");

  // History DOM
  const histHostFilter = document.getElementById("histHostFilter");
  const histTotalRxEl = document.getElementById("histTotalRx");
  const histTotalTxEl = document.getElementById("histTotalTx");
  const histCombinedEl = document.getElementById("histCombined");
  const histAvgDailyEl = document.getElementById("histAvgDaily");
  const historyCanvas = document.getElementById("historyCanvas");
  const histTooltip = document.getElementById("histTooltip");
  const histHostTableBody = document.getElementById("histHostTableBody");
  const histProcTableBody = document.getElementById("histProcTableBody");
  const customDateRow = document.getElementById("customDateRow");
  const inputStartDate = document.getElementById("inputStartDate");
  const inputEndDate = document.getElementById("inputEndDate");

  // Settings & Modal DOM
  const hostsManageTableBody = document.getElementById("hostsManageTableBody");
  const chkHideLoopback = document.getElementById("chkHideLoopback");
  const inputHistorySyncSec = document.getElementById("inputHistorySyncSec");
  const selectRetentionDays = document.getElementById("selectRetentionDays");
  const svcStateText = document.getElementById("svcStateText");
  const svcBadge = document.getElementById("svcBadge");
  const dbActionStatus = document.getElementById("dbActionStatus");

  const hostModal = document.getElementById("hostModal");
  const hostModalTitle = document.getElementById("hostModalTitle");
  const hostInputName = document.getElementById("hostInputName");
  const hostInputURL = document.getElementById("hostInputURL");
  const hostInputToken = document.getElementById("hostInputToken");
  const hostInputPollMs = document.getElementById("hostInputPollMs");
  const hostInputTags = document.getElementById("hostInputTags");
  const hostInputEnabled = document.getElementById("hostInputEnabled");
  const hostTestResult = document.getElementById("hostTestResult");

  // Utilities
  function escapeHtml(str) {
    return String(str ?? "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function formatBytes(bytes) {
    if (!bytes || bytes <= 0) return "0 B";
    const units = ["B", "KB", "MB", "GB", "TB"];
    let i = 0;
    let val = Number(bytes);
    while (val >= 1024 && i < units.length - 1) {
      val /= 1024;
      i++;
    }
    return `${val >= 100 || i === 0 ? val.toFixed(0) : val.toFixed(2)} ${units[i]}`;
  }

  function formatRate(bytesPerSec) {
    const useBits = state.settings.speedUnit === "bits";
    if (!bytesPerSec || bytesPerSec <= 0) return useBits ? "0 bps" : "0 B/s";
    if (useBits) {
      let bits = Number(bytesPerSec) * 8;
      const units = ["bps", "Kbps", "Mbps", "Gbps"];
      let i = 0;
      while (bits >= 1000 && i < units.length - 1) {
        bits /= 1000;
        i++;
      }
      return `${bits >= 100 || i === 0 ? bits.toFixed(0) : bits.toFixed(1)} ${units[i]}`;
    }
    let val = Number(bytesPerSec);
    const units = ["B/s", "KB/s", "MB/s", "GB/s"];
    let i = 0;
    while (val >= 1024 && i < units.length - 1) {
      val /= 1024;
      i++;
    }
    return `${val >= 100 || i === 0 ? val.toFixed(0) : val.toFixed(1)} ${units[i]}`;
  }

  function formatRelativeTime(unixSec) {
    if (!unixSec) return "Never";
    const diff = Math.max(0, Math.floor(Date.now() / 1000) - unixSec);
    if (diff < 5) return "Just now";
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return new Date(unixSec * 1000).toLocaleDateString();
  }

  function formatPlatform(p) {
    if (!p) return "Unknown";
    if (p === "windows") return "Win11";
    if (p === "linux") return "Linux/RPi";
    return p;
  }

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  // Theme & Settings
  function applySettingsToUI() {
    const valid = THEMES.find((t) => t.id === state.settings.theme) || THEMES[0];
    htmlEl.setAttribute("data-theme", valid.id);
    if (quickThemeLabel) quickThemeLabel.textContent = valid.label;

    document.querySelectorAll("#themeSelector .seg-btn").forEach((b) => {
      b.classList.toggle("active", b.dataset.themeVal === valid.id);
    });
    document.querySelectorAll("#unitSelector .seg-btn").forEach((b) => {
      b.classList.toggle("active", b.dataset.unitVal === state.settings.speedUnit);
    });
    if (chkHideLoopback) chkHideLoopback.checked = state.settings.hideLoopback !== false;
    if (inputHistorySyncSec && document.activeElement !== inputHistorySyncSec) {
      inputHistorySyncSec.value = state.settings.historySyncSec || 60;
    }
    if (selectRetentionDays) {
      selectRetentionDays.value = String(state.settings.retentionDays || 365);
    }

    renderOverview();
    renderExplorer();
    if (state.activeTab === "history" && state.historyReport) {
      drawHistoryChart(state.historyReport);
      renderHistoryTables(state.historyReport);
    }
  }

  async function loadSettings() {
    try {
      const res = await fetch("/api/settings");
      if (res.ok) {
        state.settings = await res.json();
        applySettingsToUI();
      }
    } catch (_) {}
  }

  async function saveSettings(patch) {
    state.settings = { ...state.settings, ...patch };
    applySettingsToUI();
    try {
      await fetch("/api/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(state.settings),
      });
    } catch (_) {}
  }

  // Navigation
  function switchTab(tab) {
    state.activeTab = tab;
    navItems.forEach((b) => b.classList.toggle("active", b.dataset.tab === tab));
    tabPanels.forEach((p) => p.classList.toggle("active", p.id === `tab-${tab}`));

    if (tab === "overview") {
      viewTitleEl.textContent = "Fleet Real-Time Overview";
      updateSubtitle();
      renderOverview();
    } else if (tab === "explorer") {
      viewTitleEl.textContent = "Host Explorer";
      ensureExplorerHostSelected();
      renderExplorer();
      refreshExplorerExtras();
    } else if (tab === "history") {
      viewTitleEl.textContent = "Fleet Traffic History";
      viewSubtitleEl.textContent = "Aggregated SQLite History";
      fetchFleetHistory();
    } else if (tab === "settings") {
      viewTitleEl.textContent = "Hosts & Sync Settings";
      viewSubtitleEl.textContent = "Configuration & Service";
      fetchHostsList();
      fetchServiceStatus();
    }
  }

  function updateSubtitle() {
    if (state.activeTab === "overview") {
      if (state.selectedHostID === "all") {
        viewSubtitleEl.textContent = "All Remote Hosts";
      } else {
        const snap = (state.cluster?.hosts || []).find((h) => h.host.id === state.selectedHostID);
        viewSubtitleEl.textContent = snap ? snap.host.name : "Host";
      }
    }
  }

  navItems.forEach((btn) => {
    btn.addEventListener("click", () => switchTab(btn.dataset.tab));
  });

  document.getElementById("btnQuickTheme").addEventListener("click", () => {
    const idx = THEMES.findIndex((t) => t.id === state.settings.theme);
    const next = THEMES[(idx + 1) % THEMES.length];
    saveSettings({ theme: next.id });
  });

  document.querySelectorAll("#themeSelector .seg-btn").forEach((b) => {
    b.addEventListener("click", () => saveSettings({ theme: b.dataset.themeVal }));
  });
  document.querySelectorAll("#unitSelector .seg-btn").forEach((b) => {
    b.addEventListener("click", () => saveSettings({ speedUnit: b.dataset.unitVal }));
  });
  if (chkHideLoopback) {
    chkHideLoopback.addEventListener("change", (e) => saveSettings({ hideLoopback: e.target.checked }));
  }
  if (inputHistorySyncSec) {
    inputHistorySyncSec.addEventListener("change", () => {
      const v = Math.max(15, Math.min(3600, parseInt(inputHistorySyncSec.value, 10) || 60));
      saveSettings({ historySyncSec: v });
    });
  }
  if (selectRetentionDays) {
    selectRetentionDays.addEventListener("change", () => {
      saveSettings({ retentionDays: parseInt(selectRetentionDays.value, 10) || 365 });
    });
  }

  // Sidebar Hosts Rendering
  function renderHostSidebar() {
    if (!hostSidebarList) return;
    const c = state.cluster;
    const hosts = c?.hosts || [];

    let html = `
      <button class="adapter-item ${state.selectedHostID === "all" ? "active" : ""}" data-sidebar-host="all">
        <div class="adapter-item-top">
          <span class="adapter-name">All Hosts (Fleet)</span>
          <span class="adapter-status-dot ${(c?.onlineHosts || 0) > 0 ? "up" : ""}"></span>
        </div>
        <div class="adapter-rates">
          <span>↓ ${escapeHtml(formatRate(c?.totalRxBps || 0))}</span>
          <span>↑ ${escapeHtml(formatRate(c?.totalTxBps || 0))}</span>
        </div>
      </button>
    `;

    for (const snap of hosts) {
      const h = snap.host;
      let dotCls = "";
      if (h.status === "online") dotCls = "up";
      else if (h.status === "auth_error") dotCls = "warn";
      else if (h.enabled) dotCls = "down";

      html += `
        <button class="adapter-item ${state.selectedHostID === h.id ? "active" : ""}" data-sidebar-host="${escapeHtml(h.id)}">
          <div class="adapter-item-top">
            <span class="adapter-name">${escapeHtml(h.name)}</span>
            <span class="adapter-status-dot ${dotCls}" title="${escapeHtml(h.status)}"></span>
          </div>
          <div class="adapter-rates">
            <span>↓ ${escapeHtml(formatRate(snap.totalRxBps || 0))}</span>
            <span>↑ ${escapeHtml(formatRate(snap.totalTxBps || 0))}</span>
          </div>
        </button>
      `;
    }
    hostSidebarList.innerHTML = html;

    hostSidebarList.querySelectorAll("button[data-sidebar-host]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.sidebarHost;
        state.selectedHostID = id;
        if (id !== "all") {
          state.explorerHostID = id;
        }
        renderHostSidebar();
        updateSubtitle();
        renderOverview();
        if (state.activeTab === "explorer" && id !== "all") {
          renderExplorer();
          refreshExplorerExtras();
        }
      });
    });
  }

  // Overview Tab
  function renderOverview() {
    if (!state.cluster) return;
    const c = state.cluster;
    const hosts = c.hosts || [];

    const selectedSnap = state.selectedHostID === "all"
      ? null
      : hosts.find((h) => h.host.id === state.selectedHostID);

    const rxRate = selectedSnap ? selectedSnap.totalRxBps : c.totalRxBps;
    const txRate = selectedSnap ? selectedSnap.totalTxBps : c.totalTxBps;
    const histPoints = selectedSnap ? (selectedSnap.history || []) : (c.totalHistory || []);

    fleetRxSpeedEl.textContent = formatRate(rxRate);
    fleetTxSpeedEl.textContent = formatRate(txRate);
    fleetHostsCountEl.textContent = `${c.onlineHosts || 0} / ${c.totalHosts || 0}`;

    if (selectedSnap) {
      const td = state.todayHostTotals[selectedSnap.host.id] || { rxBytes: 0, txBytes: 0 };
      fleetTodayRxEl.textContent = formatBytes(td.rxBytes);
      fleetTodayTxEl.textContent = formatBytes(td.txBytes);
      fleetTodayCombinedEl.textContent = formatBytes(td.rxBytes + td.txBytes);
    } else {
      fleetTodayRxEl.textContent = formatBytes(state.todayFleetRx);
      fleetTodayTxEl.textContent = formatBytes(state.todayFleetTx);
      fleetTodayCombinedEl.textContent = formatBytes(state.todayFleetRx + state.todayFleetTx);
    }

    const scaleCeiling = drawRealtimeChart(fleetCanvas, histPoints, state.fleetHover, fleetTooltip);
    if (fleetScaleMaxEl) {
      fleetScaleMaxEl.textContent = `Scale: ${formatRate(scaleCeiling)}`;
    }

    renderHostCards(hosts);
  }

  function renderHostCards(hosts) {
    if (!hostOverviewGrid) return;
    if (!hosts || hosts.length === 0) {
      hostOverviewGrid.innerHTML = `
        <div class="settings-note" style="grid-column:1/-1">
          No remote TraffCount hosts configured yet. Click <strong>+ Add Remote Host</strong> to start polling Windows 11 or Raspberry Pi nodes.
        </div>
      `;
      return;
    }

    hostOverviewGrid.innerHTML = hosts
      .map((snap) => {
        const h = snap.host;
        let badgeCls = "svc-badge";
        if (h.status === "online") badgeCls += " online";
        else if (h.status === "auth_error") badgeCls += " warn";
        else if (h.enabled) badgeCls += " offline";

        const td = state.todayHostTotals[h.id] || { rxBytes: 0, txBytes: 0 };
        const probes = snap.diagnostics?.probes || [];
        const pingText = probes.length > 0 && probes[0].online
          ? `${probes[0].latencyMs} ms`
          : (h.lastLatencyMs > 0 ? `API ${h.lastLatencyMs} ms` : "—");

        return `
          <div class="host-card" data-open-explorer="${escapeHtml(h.id)}">
            <div class="host-card-top">
              <div>
                <div class="host-card-name">${escapeHtml(h.name)}</div>
                <div class="host-card-url">${escapeHtml(h.baseUrl)}</div>
              </div>
              <span class="${badgeCls}">${escapeHtml(h.status || "pending")}</span>
            </div>
            <div class="host-card-badges">
              <span class="platform-pill">${escapeHtml(formatPlatform(h.platform))}</span>
              ${h.sourceMode ? `<span class="platform-pill">${escapeHtml(h.sourceMode)}</span>` : ""}
              ${h.tags ? `<span class="platform-pill">${escapeHtml(h.tags)}</span>` : ""}
            </div>
            <div class="host-rates-row">
              <div>
                <div class="kpi-label" style="margin-bottom:2px">Download</div>
                <div class="host-rate-val rx">${escapeHtml(formatRate(snap.totalRxBps || 0))}</div>
              </div>
              <div>
                <div class="kpi-label" style="margin-bottom:2px">Upload</div>
                <div class="host-rate-val tx">${escapeHtml(formatRate(snap.totalTxBps || 0))}</div>
              </div>
            </div>
            <div class="host-card-foot">
              <span>Today: ${escapeHtml(formatBytes(td.rxBytes + td.txBytes))}</span>
              <span>RTT: ${escapeHtml(pingText)}</span>
            </div>
          </div>
        `;
      })
      .join("");

    hostOverviewGrid.querySelectorAll(".host-card[data-open-explorer]").forEach((card) => {
      card.addEventListener("click", () => {
        state.explorerHostID = card.dataset.openExplorer;
        switchTab("explorer");
      });
    });
  }

  // Dual-Series 60s Live Canvas Renderer (matching TraffCount Desktop)
  function drawRealtimeChart(canvas, points, hoverState, tooltipEl) {
    if (!canvas) return 0;
    const rect = canvas.getBoundingClientRect();
    if (rect.width < 10 || rect.height < 10) return 0;

    const dpr = window.devicePixelRatio || 1;
    const w = Math.floor(rect.width * dpr);
    const h = Math.floor(rect.height * dpr);
    if (canvas.width !== w || canvas.height !== h) {
      canvas.width = w;
      canvas.height = h;
    }

    const ctx = canvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const cw = rect.width;
    const ch = rect.height;
    ctx.clearRect(0, 0, cw, ch);

    const padL = 10, padR = 10, padT = 14, padB = 22;
    const plotW = cw - padL - padR;
    const plotH = ch - padT - padB;

    let maxRate = 12500; // 100 Kbps minimum scale ceiling
    for (const p of points) {
      if ((p.rxBps || 0) > maxRate) maxRate = p.rxBps;
      if ((p.txBps || 0) > maxRate) maxRate = p.txBps;
    }
    const ceiling = Math.ceil(maxRate * 1.15);

    // Grid lines
    ctx.strokeStyle = cssVar("--grid-line");
    ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i++) {
      const y = padT + (plotH * i) / 4;
      ctx.beginPath();
      ctx.moveTo(padL, y);
      ctx.lineTo(cw - padR, y);
      ctx.stroke();
    }

    if (!points || points.length === 0) return ceiling;

    const n = points.length;
    const maxN = 60;
    const stepX = plotW / (maxN - 1);
    const startIdx = maxN - n;

    const drawSeries = (getter, strokeColor, fillColor) => {
      ctx.beginPath();
      for (let i = 0; i < n; i++) {
        const x = padL + (startIdx + i) * stepX;
        const val = getter(points[i]) || 0;
        const y = padT + plotH - (val / ceiling) * plotH;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      }
      ctx.strokeStyle = strokeColor;
      ctx.lineWidth = 2.2;
      ctx.stroke();

      const lastX = padL + (startIdx + n - 1) * stepX;
      const firstX = padL + startIdx * stepX;
      ctx.lineTo(lastX, padT + plotH);
      ctx.lineTo(firstX, padT + plotH);
      ctx.closePath();
      ctx.fillStyle = fillColor;
      ctx.fill();
    };

    drawSeries((p) => p.rxBps, cssVar("--rx-color"), cssVar("--rx-fill"));
    drawSeries((p) => p.txBps, cssVar("--tx-color"), cssVar("--tx-fill"));

    if (hoverState && tooltipEl) {
      const idx = Math.round((hoverState.x - padL) / stepX) - startIdx;
      if (idx >= 0 && idx < n) {
        const pt = points[idx];
        const hx = padL + (startIdx + idx) * stepX;
        ctx.strokeStyle = cssVar("--border-strong");
        ctx.setLineDash([4, 4]);
        ctx.beginPath();
        ctx.moveTo(hx, padT);
        ctx.lineTo(hx, padT + plotH);
        ctx.stroke();
        ctx.setLineDash([]);

        const tm = pt.ts ? new Date(pt.ts * 1000).toLocaleTimeString() : `${n - idx}s ago`;
        tooltipEl.innerHTML = `
          <div style="font-weight:600;margin-bottom:4px">${escapeHtml(tm)}</div>
          <div style="color:var(--rx-color)">Download: ${escapeHtml(formatRate(pt.rxBps || 0))}</div>
          <div style="color:var(--tx-color)">Upload: ${escapeHtml(formatRate(pt.txBps || 0))}</div>
        `;
        tooltipEl.classList.remove("hidden");
        tooltipEl.style.left = `${Math.min(cw - 165, Math.max(8, hoverState.x + 12))}px`;
        tooltipEl.style.top = `${Math.max(8, hoverState.y - 50)}px`;
      } else {
        tooltipEl.classList.add("hidden");
      }
    }
    return ceiling;
  }

  if (fleetCanvas) {
    fleetCanvas.addEventListener("mousemove", (e) => {
      const r = fleetCanvas.getBoundingClientRect();
      state.fleetHover = { x: e.clientX - r.left, y: e.clientY - r.top };
      renderOverview();
    });
    fleetCanvas.addEventListener("mouseleave", () => {
      state.fleetHover = null;
      fleetTooltip?.classList.add("hidden");
      renderOverview();
    });
  }

  // Host Explorer Tab
  function syncHostDropdowns() {
    const hosts = state.cluster?.hosts || state.hostsList.map((h) => ({ host: h }));
    if (explorerHostSelect) {
      const prev = state.explorerHostID || explorerHostSelect.value;
      explorerHostSelect.innerHTML = hosts.length === 0
        ? `<option value="">No hosts registered</option>`
        : hosts.map((s) => `<option value="${escapeHtml(s.host.id)}">${escapeHtml(s.host.name)} (${escapeHtml(formatPlatform(s.host.platform))})</option>`).join("");
      if (prev && hosts.some((s) => s.host.id === prev)) {
        explorerHostSelect.value = prev;
        state.explorerHostID = prev;
      } else if (hosts.length > 0) {
        state.explorerHostID = hosts[0].host.id;
        explorerHostSelect.value = state.explorerHostID;
      }
    }

    if (histHostFilter) {
      const prevH = state.historyFilter.hostId || "all";
      histHostFilter.innerHTML =
        `<option value="all">All Remote Hosts</option>` +
        hosts.map((s) => `<option value="${escapeHtml(s.host.id)}">${escapeHtml(s.host.name)}</option>`).join("");
      histHostFilter.value = hosts.some((s) => s.host.id === prevH) || prevH === "all" ? prevH : "all";
    }
  }

  function ensureExplorerHostSelected() {
    const hosts = state.cluster?.hosts || [];
    if (!state.explorerHostID && hosts.length > 0) {
      state.explorerHostID = state.selectedHostID !== "all" ? state.selectedHostID : hosts[0].host.id;
    }
    syncHostDropdowns();
  }

  if (explorerHostSelect) {
    explorerHostSelect.addEventListener("change", () => {
      state.explorerHostID = explorerHostSelect.value;
      renderExplorer();
      refreshExplorerExtras();
    });
  }

  function renderExplorer() {
    if (state.activeTab !== "explorer") return;
    ensureExplorerHostSelected();
    const snap = (state.cluster?.hosts || []).find((s) => s.host.id === state.explorerHostID);
    if (!snap) {
      explorerHostMeta.textContent = "No host selected";
      return;
    }
    const h = snap.host;
    explorerHostMeta.textContent = `${h.baseUrl} · ${formatPlatform(h.platform)} · v${h.version || "?"}`;
    expRxSpeedEl.textContent = formatRate(snap.totalRxBps || 0);
    expTxSpeedEl.textContent = formatRate(snap.totalTxBps || 0);
    expSessRxEl.textContent = formatBytes(snap.sessionRx || 0);
    expSessTxEl.textContent = formatBytes(snap.sessionTx || 0);
    expPollLatencyEl.textContent = h.lastLatencyMs > 0 ? `${h.lastLatencyMs} ms` : "—";
    expSourceModeEl.textContent = h.sourceMode || "unknown";

    const upIfaces = (snap.interfaces || []).filter((i) => i.isUp && !i.isLoopback);
    expNicCountEl.textContent = String(upIfaces.length || (snap.interfaces || []).length);
    expPrimaryNicEl.textContent = upIfaces[0]?.name || (snap.interfaces?.[0]?.name || "—");

    if (expPingBadgesEl) {
      const probes = snap.diagnostics?.probes || [];
      expPingBadgesEl.innerHTML = probes
        .map(
          (p) =>
            `<span class="ping-badge ${p.online ? "ok" : "bad"}">${escapeHtml(p.name)}: <strong>${p.online ? `${p.latencyMs}ms` : "Timeout"}</strong></span>`
        )
        .join("");
    }

    drawRealtimeChart(explorerCanvas, snap.history || [], state.explorerHover, explorerTooltip);
  }

  if (explorerCanvas) {
    explorerCanvas.addEventListener("mousemove", (e) => {
      const r = explorerCanvas.getBoundingClientRect();
      state.explorerHover = { x: e.clientX - r.left, y: e.clientY - r.top };
      renderExplorer();
    });
    explorerCanvas.addEventListener("mouseleave", () => {
      state.explorerHover = null;
      explorerTooltip?.classList.add("hidden");
      renderExplorer();
    });
  }

  async function refreshExplorerExtras() {
    const id = state.explorerHostID;
    if (!id) return;
    fetchHostQuota(id);
    fetchHostConnections(id);
    fetchHostSpeedTests(id);
  }

  async function fetchHostQuota(id) {
    try {
      const res = await fetch(`/api/hosts/${encodeURIComponent(id)}/quota`);
      if (!res.ok) {
        explorerQuotaBanner?.classList.add("hidden");
        return;
      }
      const q = await res.json();
      if (!q.enabled) {
        explorerQuotaBanner?.classList.add("hidden");
        return;
      }
      explorerQuotaBanner?.classList.remove("hidden");
      explorerQuotaCycle.textContent = q.cycleLabel || "Current cycle";
      explorerQuotaNumbers.textContent = `${q.usedGB.toFixed(2)} GB / ${q.capGB.toFixed(0)} GB (${q.pctUsed.toFixed(1)}%)`;
      explorerQuotaFill.style.width = `${Math.min(100, q.pctUsed)}%`;
      explorerQuotaProjection.textContent = `Projected end-of-cycle usage: ${q.projectedGB.toFixed(1)} GB (${q.projectedPct.toFixed(0)}%)`;
    } catch (_) {
      explorerQuotaBanner?.classList.add("hidden");
    }
  }

  async function fetchHostConnections(id) {
    if (!expProcTableBody) return;
    try {
      const res = await fetch(`/api/hosts/${encodeURIComponent(id)}/connections`);
      if (!res.ok) {
        expProcTableBody.innerHTML = `<tr><td colspan="6" style="color:var(--text-muted)">Unable to reach remote agent</td></tr>`;
        return;
      }
      const snap = await res.json();
      const procs = snap.topProcesses || [];
      if (procs.length === 0) {
        expProcTableBody.innerHTML = `<tr><td colspan="6" style="color:var(--text-muted)">No active application sockets reported</td></tr>`;
        return;
      }
      expProcTableBody.innerHTML = procs
        .slice(0, 25)
        .map(
          (p) => `
        <tr>
          <td><strong>${escapeHtml(p.processName)}</strong></td>
          <td>${p.pid}</td>
          <td>${p.established}</td>
          <td class="num">${escapeHtml(formatRate(p.rxBps))}</td>
          <td class="num">${escapeHtml(formatRate(p.txBps))}</td>
          <td class="num">${escapeHtml(formatBytes((p.totalRx || 0) + (p.totalTx || 0)))}</td>
        </tr>
      `
        )
        .join("");
    } catch (_) {}
  }

  async function fetchHostSpeedTests(id) {
    if (!expSpeedTestBody) return;
    try {
      const res = await fetch(`/api/speedtest/history?hostId=${encodeURIComponent(id)}&limit=15`);
      if (!res.ok) return;
      const list = await res.json();
      if (!list || list.length === 0) {
        expSpeedTestBody.innerHTML = `<tr><td colspan="5" style="color:var(--text-muted)">No speed tests synced for this host yet.</td></tr>`;
        return;
      }
      expSpeedTestBody.innerHTML = list
        .map(
          (r) => `
        <tr>
          <td>${escapeHtml(new Date(r.timestamp * 1000).toLocaleString())}</td>
          <td class="num">${r.downloadMbps.toFixed(1)} Mbps</td>
          <td class="num">${r.uploadMbps.toFixed(1)} Mbps</td>
          <td class="num">${r.unloadedPingMs.toFixed(0)} / ${r.loadedPingMs.toFixed(0)} ms</td>
          <td><span class="svc-badge running">${escapeHtml(r.bufferbloatGrade || "—")}</span></td>
        </tr>
      `
        )
        .join("");
    } catch (_) {}
  }

  document.getElementById("btnRefreshExpConns").addEventListener("click", () => {
    if (state.explorerHostID) fetchHostConnections(state.explorerHostID);
  });

  document.getElementById("btnExplorerSync").addEventListener("click", async () => {
    if (!state.explorerHostID) return;
    await fetch(`/api/hosts/${encodeURIComponent(state.explorerHostID)}/sync`, { method: "POST" });
    setTimeout(refreshExplorerExtras, 700);
  });

  document.getElementById("btnExplorerSpeedTest").addEventListener("click", async (e) => {
    if (!state.explorerHostID) return;
    const btn = e.currentTarget;
    btn.disabled = true;
    btn.textContent = "Running on Host...";
    if (expSpeedTestStatus) expSpeedTestStatus.textContent = "Running Cloudflare speed & bufferbloat probe on remote host...";
    try {
      const res = await fetch(`/api/hosts/${encodeURIComponent(state.explorerHostID)}/speedtest`, { method: "POST" });
      if (res.ok) {
        await fetchHostSpeedTests(state.explorerHostID);
        if (expSpeedTestStatus) expSpeedTestStatus.textContent = "Remote speed test completed and saved.";
      } else {
        const err = await res.json().catch(() => ({}));
        if (expSpeedTestStatus) expSpeedTestStatus.textContent = err.error || "Remote speed test failed.";
      }
    } finally {
      btn.disabled = false;
      btn.textContent = "Run Remote Speedtest";
    }
  });

  // Fleet History Tab
  function buildHistoryQueryString() {
    const p = new URLSearchParams();
    p.set("hostId", state.historyFilter.hostId || "all");
    if (state.historyFilter.period) {
      p.set("period", state.historyFilter.period);
    } else if (state.historyFilter.start && state.historyFilter.end) {
      p.set("start", state.historyFilter.start);
      p.set("end", state.historyFilter.end);
    } else {
      p.set("days", String(state.historyFilter.days || 14));
    }
    return p.toString();
  }

  async function refreshTodayTotals() {
    const now = Date.now();
    if (now - state.lastTodayFetchAt < 10000) return;
    state.lastTodayFetchAt = now;
    try {
      const res = await fetch("/api/history?period=today&hostId=all");
      if (!res.ok) return;
      const rep = await res.json();
      state.todayFleetRx = rep.totalRxBytes || 0;
      state.todayFleetTx = rep.totalTxBytes || 0;
      const map = {};
      for (const h of rep.byHost || []) {
        map[h.hostId] = { rxBytes: h.rxBytes, txBytes: h.txBytes };
      }
      state.todayHostTotals = map;
    } catch (_) {}
  }

  async function fetchFleetHistory() {
    try {
      const res = await fetch(`/api/history?${buildHistoryQueryString()}`);
      if (!res.ok) return;
      const rep = await res.json();
      state.historyReport = rep;
      histTotalRxEl.textContent = formatBytes(rep.totalRxBytes);
      histTotalTxEl.textContent = formatBytes(rep.totalTxBytes);
      histCombinedEl.textContent = formatBytes(rep.combinedBytes);
      histAvgDailyEl.textContent = formatBytes(rep.avgDailyBytes);
      drawHistoryChart(rep);
      renderHistoryTables(rep);
    } catch (_) {}
  }

  function renderHistoryTables(rep) {
    if (histHostTableBody) {
      const byHost = rep.byHost || [];
      histHostTableBody.innerHTML = byHost.length === 0
        ? `<tr><td colspan="6" style="color:var(--text-muted)">No synced host traffic for this range.</td></tr>`
        : byHost
            .map(
              (h) => `
            <tr>
              <td><strong>${escapeHtml(h.hostName)}</strong></td>
              <td><span class="platform-pill">${escapeHtml(formatPlatform(h.platform))}</span></td>
              <td class="num">${escapeHtml(formatBytes(h.rxBytes))}</td>
              <td class="num">${escapeHtml(formatBytes(h.txBytes))}</td>
              <td class="num"><strong>${escapeHtml(formatBytes(h.totalBytes))}</strong></td>
              <td class="num">${h.sharePct.toFixed(1)}%</td>
            </tr>
          `
            )
            .join("");
    }

    if (histProcTableBody) {
      const byProc = rep.byProcess || [];
      histProcTableBody.innerHTML = byProc.length === 0
        ? `<tr><td colspan="6" style="color:var(--text-muted)">No per-application history synced for this range yet.</td></tr>`
        : byProc
            .map(
              (p) => `
            <tr>
              <td><span class="platform-pill">${escapeHtml(p.hostName)}</span></td>
              <td><span title="${escapeHtml(p.exePath || p.processName)}">${escapeHtml(p.processName)}</span></td>
              <td class="num">${escapeHtml(formatBytes(p.rxBytes))}</td>
              <td class="num">${escapeHtml(formatBytes(p.txBytes))}</td>
              <td class="num"><strong>${escapeHtml(formatBytes(p.totalBytes))}</strong></td>
              <td class="num">${p.sharePct.toFixed(1)}%</td>
            </tr>
          `
            )
            .join("");
    }
  }

  function drawHistoryChart(rep) {
    if (!historyCanvas) return;
    const rect = historyCanvas.getBoundingClientRect();
    if (rect.width < 10 || rect.height < 10) return;

    const dpr = window.devicePixelRatio || 1;
    const w = Math.floor(rect.width * dpr);
    const h = Math.floor(rect.height * dpr);
    if (historyCanvas.width !== w || historyCanvas.height !== h) {
      historyCanvas.width = w;
      historyCanvas.height = h;
    }
    const ctx = historyCanvas.getContext("2d");
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const cw = rect.width;
    const ch = rect.height;
    ctx.clearRect(0, 0, cw, ch);

    const series = rep.series || [];
    const padL = 62, padR = 16, padT = 16, padB = 34;
    const plotW = cw - padL - padR;
    const plotH = ch - padT - padB;

    let maxTotal = 1024 * 1024;
    for (const pt of series) {
      if ((pt.total || 0) > maxTotal) maxTotal = pt.total;
    }

    ctx.strokeStyle = cssVar("--grid-line");
    ctx.fillStyle = cssVar("--text-muted");
    ctx.font = "11px sans-serif";
    ctx.lineWidth = 1;

    for (let i = 0; i <= 4; i++) {
      const y = padT + (plotH * i) / 4;
      ctx.beginPath();
      ctx.moveTo(padL, y);
      ctx.lineTo(cw - padR, y);
      ctx.stroke();
      const val = maxTotal * (1 - i / 4);
      ctx.fillText(formatBytes(val), 6, y + 4);
    }

    if (series.length === 0) return;
    const n = series.length;
    const slotW = plotW / n;
    const barW = Math.max(4, Math.min(34, slotW * 0.64));

    const rxCol = cssVar("--rx-color");
    const txCol = cssVar("--tx-color");
    const labelStep = Math.max(1, Math.ceil(n / 10));

    for (let i = 0; i < n; i++) {
      const pt = series[i];
      const bx = padL + i * slotW + (slotW - barW) / 2;
      const rxH = (pt.rxBytes / maxTotal) * plotH;
      const txH = (pt.txBytes / maxTotal) * plotH;

      ctx.fillStyle = rxCol;
      ctx.fillRect(bx, padT + plotH - rxH, barW, rxH);
      ctx.fillStyle = txCol;
      ctx.fillRect(bx, padT + plotH - rxH - txH, barW, txH);

      if (i % labelStep === 0) {
        ctx.fillStyle = cssVar("--text-muted");
        ctx.fillText(pt.label, bx, ch - 10);
      }
    }

    if (state.histHover && histTooltip) {
      const idx = Math.floor((state.histHover.x - padL) / slotW);
      if (idx >= 0 && idx < n) {
        const pt = series[idx];
        histTooltip.innerHTML = `
          <div style="font-weight:600;margin-bottom:4px">${escapeHtml(pt.label)}</div>
          <div style="color:var(--rx-color)">Download: ${escapeHtml(formatBytes(pt.rxBytes))}</div>
          <div style="color:var(--tx-color)">Upload: ${escapeHtml(formatBytes(pt.txBytes))}</div>
          <div style="margin-top:3px;border-top:1px solid var(--border-color);padding-top:3px">Total: <strong>${escapeHtml(formatBytes(pt.total))}</strong></div>
        `;
        histTooltip.classList.remove("hidden");
        histTooltip.style.left = `${Math.min(cw - 175, Math.max(8, state.histHover.x + 12))}px`;
        histTooltip.style.top = `${Math.max(8, state.histHover.y - 45)}px`;
      } else {
        histTooltip.classList.add("hidden");
      }
    }
  }

  if (historyCanvas) {
    historyCanvas.addEventListener("mousemove", (e) => {
      const r = historyCanvas.getBoundingClientRect();
      state.histHover = { x: e.clientX - r.left, y: e.clientY - r.top };
      if (state.historyReport) drawHistoryChart(state.historyReport);
    });
    historyCanvas.addEventListener("mouseleave", () => {
      state.histHover = null;
      histTooltip?.classList.add("hidden");
      if (state.historyReport) drawHistoryChart(state.historyReport);
    });
  }

  document.querySelectorAll("#historyRangePills .pill").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll("#historyRangePills .pill").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      const r = btn.dataset.range;
      customDateRow.classList.toggle("hidden", r !== "custom");
      state.historyFilter.period = "";
      state.historyFilter.start = "";
      state.historyFilter.end = "";
      if (r === "today" || r === "24h") {
        state.historyFilter.period = r;
        fetchFleetHistory();
      } else if (r !== "custom") {
        state.historyFilter.days = parseInt(r, 10) || 14;
        fetchFleetHistory();
      }
    });
  });

  document.getElementById("btnApplyCustomRange").addEventListener("click", () => {
    if (!inputStartDate.value || !inputEndDate.value) return;
    state.historyFilter.start = inputStartDate.value;
    state.historyFilter.end = inputEndDate.value;
    fetchFleetHistory();
  });

  if (histHostFilter) {
    histHostFilter.addEventListener("change", () => {
      state.historyFilter.hostId = histHostFilter.value;
      fetchFleetHistory();
    });
  }

  document.getElementById("btnExportCSV").addEventListener("click", () => {
    window.location.href = `/api/history/export?format=csv&${buildHistoryQueryString()}`;
  });
  document.getElementById("btnExportJSON").addEventListener("click", () => {
    window.location.href = `/api/history/export?format=json&${buildHistoryQueryString()}`;
  });

  // Hosts Management & Modal
  async function fetchHostsList() {
    try {
      const res = await fetch("/api/hosts");
      if (!res.ok) return;
      state.hostsList = await res.json();
      syncHostDropdowns();
      renderManageHostsTable();
    } catch (_) {}
  }

  function renderManageHostsTable() {
    if (!hostsManageTableBody) return;
    if (!state.hostsList || state.hostsList.length === 0) {
      hostsManageTableBody.innerHTML = `<tr><td colspan="7" style="color:var(--text-muted)">No remote hosts registered yet.</td></tr>`;
      return;
    }
    hostsManageTableBody.innerHTML = state.hostsList
      .map((h) => {
        let cls = "svc-badge";
        if (h.status === "online") cls += " online";
        else if (h.status === "auth_error") cls += " warn";
        else if (h.enabled) cls += " offline";

        return `
          <tr>
            <td><strong>${escapeHtml(h.name)}</strong>${h.tags ? `<div style="font-size:11px;color:var(--text-muted)">${escapeHtml(h.tags)}</div>` : ""}</td>
            <td><code>${escapeHtml(h.baseUrl)}</code>${h.hasToken ? ` <span class="platform-pill">Token</span>` : ""}</td>
            <td>${escapeHtml(formatPlatform(h.platform))} ${h.sourceMode ? `(${escapeHtml(h.sourceMode)})` : ""}</td>
            <td>
              <span class="${cls}">${escapeHtml(h.status)}</span>
              ${h.lastError ? `<div style="font-size:11px;color:var(--danger);max-width:220px;overflow:hidden;text-overflow:ellipsis">${escapeHtml(h.lastError)}</div>` : ""}
            </td>
            <td>${h.pollIntervalMs} ms</td>
            <td>${escapeHtml(formatRelativeTime(h.lastSyncTs))}</td>
            <td class="num">
              <button class="btn-secondary btn-sm" data-host-sync="${escapeHtml(h.id)}">Sync</button>
              <button class="btn-secondary btn-sm" data-host-edit="${escapeHtml(h.id)}">Edit</button>
              <button class="btn-danger btn-sm" data-host-del="${escapeHtml(h.id)}">Remove</button>
            </td>
          </tr>
        `;
      })
      .join("");

    hostsManageTableBody.querySelectorAll("button[data-host-sync]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        await fetch(`/api/hosts/${encodeURIComponent(btn.dataset.hostSync)}/sync`, { method: "POST" });
        setTimeout(() => {
          btn.disabled = false;
          fetchHostsList();
        }, 800);
      });
    });

    hostsManageTableBody.querySelectorAll("button[data-host-edit]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const h = state.hostsList.find((x) => x.id === btn.dataset.hostEdit);
        if (h) openHostModal(h);
      });
    });

    hostsManageTableBody.querySelectorAll("button[data-host-del]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        if (!confirm("Remove this host and all its synced fleet history from TraffCount Sync?")) return;
        await fetch(`/api/hosts/${encodeURIComponent(btn.dataset.hostDel)}`, { method: "DELETE" });
        if (state.selectedHostID === btn.dataset.hostDel) state.selectedHostID = "all";
        if (state.explorerHostID === btn.dataset.hostDel) state.explorerHostID = "";
        fetchHostsList();
      });
    });
  }

  function openHostModal(existingHost = null) {
    state.editingHostID = existingHost ? existingHost.id : "";
    hostModalTitle.textContent = existingHost ? `Edit Host: ${existingHost.name}` : "Add Remote TraffCount Host";
    hostInputName.value = existingHost ? existingHost.name : "";
    hostInputURL.value = existingHost ? existingHost.baseUrl : "";
    hostInputToken.value = "";
    hostInputToken.placeholder = existingHost?.hasToken ? "Leave blank to keep saved token" : "Leave blank if host has no API token";
    hostInputPollMs.value = String(existingHost?.pollIntervalMs || 1000);
    hostInputTags.value = existingHost?.tags || "";
    hostInputEnabled.checked = existingHost ? !!existingHost.enabled : true;
    hostTestResult.classList.add("hidden");
    hostModal.classList.remove("hidden");
  }

  function closeHostModal() {
    hostModal.classList.add("hidden");
  }

  ["btnSidebarAddHost", "btnQuickAddHost", "btnAddHostEmpty", "btnSettingsAddHost"].forEach((id) => {
    const el = document.getElementById(id);
    if (el) el.addEventListener("click", () => openHostModal(null));
  });
  document.getElementById("btnCloseHostModal").addEventListener("click", closeHostModal);

  document.getElementById("btnTestHostConn").addEventListener("click", async () => {
    const baseUrl = hostInputURL.value.trim();
    if (!baseUrl) return;
    hostTestResult.classList.remove("hidden");
    hostTestResult.textContent = "Testing connection to remote host...";
    try {
      const res = await fetch("/api/hosts/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ baseUrl, apiToken: hostInputToken.value.trim() }),
      });
      const r = await res.json();
      if (r.ok) {
        if (!hostInputName.value.trim() && r.hostname) {
          hostInputName.value = r.hostname;
        }
        hostTestResult.innerHTML = `<strong style="color:var(--success)">Connected!</strong> Platform: <code>${escapeHtml(formatPlatform(r.platform))}</code>, Version: <code>${escapeHtml(r.version)}</code>, Source: <code>${escapeHtml(r.source)}</code> (${r.latencyMs} ms)`;
      } else {
        hostTestResult.innerHTML = `<strong style="color:var(--danger)">Failed:</strong> ${escapeHtml(r.error || "Unreachable")}`;
      }
    } catch (e) {
      hostTestResult.innerHTML = `<strong style="color:var(--danger)">Error:</strong> ${escapeHtml(e.message)}`;
    }
  });

  document.getElementById("btnSaveHostModal").addEventListener("click", async () => {
    const payload = {
      name: hostInputName.value.trim(),
      baseUrl: hostInputURL.value.trim(),
      apiToken: hostInputToken.value.trim(),
      keepToken: !!state.editingHostID && hostInputToken.value.trim() === "",
      pollIntervalMs: parseInt(hostInputPollMs.value, 10) || 1000,
      tags: hostInputTags.value.trim(),
      enabled: hostInputEnabled.checked,
    };
    if (!payload.baseUrl) {
      alert("Please enter the host URL or IP:Port.");
      return;
    }
    const url = state.editingHostID ? `/api/hosts/${encodeURIComponent(state.editingHostID)}` : "/api/hosts";
    const method = state.editingHostID ? "PUT" : "POST";
    const res = await fetch(url, {
      method,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      alert(err.error || "Failed to save host.");
      return;
    }
    closeHostModal();
    await fetchHostsList();
  });

  // DB Backup & Reset
  document.getElementById("btnBackupDB").addEventListener("click", () => {
    window.location.href = "/api/db/backup";
  });

  document.getElementById("btnResetDB").addEventListener("click", async () => {
    if (!confirm("Reset all stored historical traffic, app rollups, and speed tests in TraffCount Sync? Registered hosts and settings will remain.")) {
      return;
    }
    const res = await fetch("/api/db/reset", { method: "POST" });
    if (res.ok) {
      dbActionStatus.textContent = "Fleet traffic history cleared.";
      state.todayFleetRx = 0;
      state.todayFleetTx = 0;
      state.todayHostTotals = {};
      fetchFleetHistory();
    }
  });

  // Service Lifecycle
  function updateSourceBadge(source, platform) {
    state.source = source === "service" ? "service" : "server";
    if (platform) state.platform = platform;
    const isSvc = state.source === "service";
    if (dataSourceBadge) {
      dataSourceBadge.textContent = isSvc ? "Source: Service" : "Source: Server";
      dataSourceBadge.classList.toggle("service", isSvc);
      dataSourceBadge.classList.toggle("app", !isSvc);
    }
    if (platform && platform !== "windows") {
      const svcTitle = document.getElementById("svcCardTitle");
      const svcCliTip = document.getElementById("svcCliTip");
      if (svcTitle) svcTitle.textContent = "Linux Sync Background Service (systemd)";
      if (svcCliTip) {
        svcCliTip.innerHTML =
          "<strong>CLI Tip:</strong> Manage the service via SSH using: <code>sudo traffcount-sync service install</code> or <code>traffcount-sync service status</code>";
      }
    }
  }

  async function fetchServiceStatus() {
    try {
      const [res, pingRes] = await Promise.all([fetch("/api/service"), fetch("/api/ping")]);
      if (pingRes && pingRes.ok) {
        const p = await pingRes.json();
        updateSourceBadge(p.source, p.platform);
      }
      if (!res || !res.ok) return;
      const st = await res.json();
      const svcName = state.platform === "windows" ? "TraffCountSyncService" : "traffcount-sync.service";
      svcStateText.textContent = st.installed
        ? `${svcName} is ${st.stateText}`
        : `${svcName} is not installed (Running in standalone server mode)`;
      svcBadge.textContent = st.stateText || "Unknown";
      svcBadge.classList.toggle("running", !!st.running);
    } catch (_) {}
  }

  async function invokeServiceAction(action) {
    try {
      await fetch("/api/service", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action }),
      });
    } catch (_) {}
    for (let i = 1; i <= 12; i++) {
      setTimeout(fetchServiceStatus, i * 400);
    }
  }

  document.getElementById("btnSvcInstall").addEventListener("click", () => invokeServiceAction("install"));
  document.getElementById("btnSvcStart").addEventListener("click", () => invokeServiceAction("start"));
  document.getElementById("btnSvcStop").addEventListener("click", () => invokeServiceAction("stop"));
  document.getElementById("btnSvcUninstall").addEventListener("click", () => invokeServiceAction("uninstall"));

  // WebSocket Live Stream
  function connectWebSocket() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/ws`);
    const dot = document.querySelector("#wsStatus .status-dot");
    const txt = document.getElementById("wsStatusText");

    ws.onopen = () => {
      if (dot) {
        dot.classList.remove("offline");
        dot.classList.add("online");
      }
      if (txt) txt.textContent = "Live (1s)";
    };

    ws.onmessage = (evt) => {
      try {
        const payload = JSON.parse(evt.data);
        state.cluster = payload;
        if (payload.source) updateSourceBadge(payload.source, state.platform);
        syncHostDropdowns();
        renderHostSidebar();
        refreshTodayTotals();
        if (state.activeTab === "overview") renderOverview();
        else if (state.activeTab === "explorer") renderExplorer();
      } catch (_) {}
    };

    ws.onclose = () => {
      if (dot) {
        dot.classList.remove("online");
        dot.classList.add("offline");
      }
      if (txt) txt.textContent = "Reconnecting...";
      setTimeout(connectWebSocket, 1500);
    };
  }

  // Default custom range values
  const todayStr = new Date().toISOString().slice(0, 10);
  const twoWeeksAgoStr = new Date(Date.now() - 13 * 86400000).toISOString().slice(0, 10);
  if (inputStartDate) inputStartDate.value = twoWeeksAgoStr;
  if (inputEndDate) inputEndDate.value = todayStr;

  // Boot
  Promise.all([loadSettings(), fetchHostsList(), fetchServiceStatus(), refreshTodayTotals()]).then(() => {
    connectWebSocket();
  });
})();
