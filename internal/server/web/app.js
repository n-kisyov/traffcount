(() => {
  // Application State
  const state = {
    activeTab: "realtime",
    selectedIface: "all", // "all" or specific NIC id
    settings: {
      theme: "dark",
      alwaysOnTop: false,
      startOnBoot: false,
      startMinimized: false,
      speedUnit: "bits",
      hideLoopback: true,
      retentionDays: 365,
      refreshRateMs: 1000,
      dataCapEnabled: false,
      dataCapGB: 500,
      dataCapIface: "all",
      billingDay: 1,
      spikeAlertMbps: 0,
      newAppAlertEnabled: false,
    },
    miniMode: false,
    latestRealtime: null,
    historyFilter: {
      period: "",
      days: 14,
      start: "",
      end: "",
      iface: "all",
    },
    historyReport: null,
    connectionsSnap: null,
    connFilter: {
      subview: "sockets", // "sockets" or "processes"
      state: "ALL",       // "ALL", "ESTABLISHED", "LISTEN"
      includeUDP: false,
      search: "",
    },
    rtHover: null,        // { x, y, index }
    histHover: null,      // { x, y, index }
    currentRtPoints: [],
  };

  // DOM Elements
  const htmlEl = document.documentElement;
  const navItems = document.querySelectorAll(".nav-item");
  const tabPanels = document.querySelectorAll(".tab-panel");
  const adapterListEl = document.getElementById("adapterList");
  const viewTitleEl = document.getElementById("viewTitle");
  const viewSubtitleEl = document.getElementById("viewSubtitle");
  const btnMiniWidget = document.getElementById("btnMiniWidget");
  const miniWidgetLabel = document.getElementById("miniWidgetLabel");
  const btnStayOnTop = document.getElementById("btnStayOnTop");
  const btnQuickTheme = document.getElementById("btnQuickTheme");
  const quickThemeLabel = document.getElementById("quickThemeLabel");

  const THEMES = [
    { id: "dark", label: "Win11 Dark" },
    { id: "antigravity", label: "Antigravity IDE" },
    { id: "nord", label: "Nord" },
    { id: "dracula", label: "Dracula" },
    { id: "emerald", label: "Emerald" },
    { id: "light", label: "Light" },
  ];

  // Realtime DOM
  const rtRxSpeedEl = document.getElementById("rtRxSpeed");
  const rtTxSpeedEl = document.getElementById("rtTxSpeed");
  const rtSessionRxEl = document.getElementById("rtSessionRx");
  const rtSessionTxEl = document.getElementById("rtSessionTx");
  const rtRxPpsEl = document.getElementById("rtRxPps");
  const rtTxPpsEl = document.getElementById("rtTxPps");
  const rtLinkSpeedEl = document.getElementById("rtLinkSpeed");
  const rtIPv4El = document.getElementById("rtIPv4");
  const rtMACEl = document.getElementById("rtMAC");
  const rtPingBadgesEl = document.getElementById("rtPingBadges");
  const rtAdapterDescEl = document.getElementById("rtAdapterDesc");
  const rtScaleMaxEl = document.getElementById("rtScaleMax");
  const rtCanvas = document.getElementById("realtimeCanvas");
  const rtTooltip = document.getElementById("rtTooltip");

  // Connections Inspector DOM
  const connCountEstEl = document.getElementById("connCountEst");
  const connCountListenEl = document.getElementById("connCountListen");
  const connCountWaitEl = document.getElementById("connCountWait");
  const connCountUdpWrapEl = document.getElementById("connCountUdpWrap");
  const connCountUdpEl = document.getElementById("connCountUdp");
  const connSubTabBtns = document.querySelectorAll("#connSubTabSelector .seg-btn");
  const connStateBtns = document.querySelectorAll("#connStateFilter .seg-btn");
  const chkIncludeUDP = document.getElementById("chkIncludeUDP");
  const connSearchInput = document.getElementById("connSearchInput");
  const connSocketsTable = document.getElementById("connSocketsTable");
  const connProcessesTable = document.getElementById("connProcessesTable");
  const connTableBody = document.getElementById("connTableBody");
  const connProcTableBody = document.getElementById("connProcTableBody");

  // History DOM
  const periodPills = document.querySelectorAll("#periodPills .pill-btn");
  const customDateBox = document.getElementById("customDateBox");
  const inputStartDate = document.getElementById("inputStartDate");
  const inputEndDate = document.getElementById("inputEndDate");
  const btnApplyDates = document.getElementById("btnApplyDates");
  const historyIfaceSelect = document.getElementById("historyIfaceSelect");
  const btnExportCSV = document.getElementById("btnExportCSV");
  const btnExportJSON = document.getElementById("btnExportJSON");
  const quotaBanner = document.getElementById("quotaBanner");
  const quotaCycleText = document.getElementById("quotaCycleText");
  const quotaUsedText = document.getElementById("quotaUsedText");
  const quotaPctBadge = document.getElementById("quotaPctBadge");
  const quotaBarFill = document.getElementById("quotaBarFill");
  const histTotalRxEl = document.getElementById("histTotalRx");
  const histTotalTxEl = document.getElementById("histTotalTx");
  const histCombinedEl = document.getElementById("histCombined");
  const histAvgDailyEl = document.getElementById("histAvgDaily");
  const histPeriodLabelEl = document.getElementById("histPeriodLabel");
  const histGranularityLabelEl = document.getElementById("histGranularityLabel");
  const historyIfaceTableBody = document.getElementById("historyIfaceTableBody");
  const historyProcTableBody = document.getElementById("historyProcTableBody");
  const histCanvas = document.getElementById("historyCanvas");
  const histTooltip = document.getElementById("histTooltip");

  // Speed Test DOM
  const btnRunSpeedTest = document.getElementById("btnRunSpeedTest");
  const stStatusText = document.getElementById("stStatusText");
  const stDownMbps = document.getElementById("stDownMbps");
  const stUpMbps = document.getElementById("stUpMbps");
  const stUnloadedPing = document.getElementById("stUnloadedPing");
  const stLoadedPing = document.getElementById("stLoadedPing");
  const stJitter = document.getElementById("stJitter");
  const stGradeBadge = document.getElementById("stGradeBadge");

  // Settings DOM
  const themeButtons = document.querySelectorAll("#themeSelector .seg-btn");
  const unitButtons = document.querySelectorAll("#unitSelector .seg-btn");
  const chkAlwaysOnTop = document.getElementById("chkAlwaysOnTop");
  const chkStartOnBoot = document.getElementById("chkStartOnBoot");
  const chkStartMinimized = document.getElementById("chkStartMinimized");
  const chkHideLoopback = document.getElementById("chkHideLoopback");
  const chkDataCapEnabled = document.getElementById("chkDataCapEnabled");
  const inputDataCapGB = document.getElementById("inputDataCapGB");
  const inputBillingDay = document.getElementById("inputBillingDay");
  const inputSpikeMbps = document.getElementById("inputSpikeMbps");
  const chkNewAppAlert = document.getElementById("chkNewAppAlert");
  const btnBackupDB = document.getElementById("btnBackupDB");
  const btnResetDB = document.getElementById("btnResetDB");
  const dbActionStatus = document.getElementById("dbActionStatus");
  const svcStateText = document.getElementById("svcStateText");
  const svcBadge = document.getElementById("svcBadge");

  // Formatting Helpers
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
    if (!bytesPerSec || bytesPerSec <= 0) {
      return useBits ? "0 bps" : "0 B/s";
    }
    if (useBits) {
      let bits = Number(bytesPerSec) * 8;
      const units = ["bps", "Kbps", "Mbps", "Gbps"];
      let i = 0;
      while (bits >= 1000 && i < units.length - 1) {
        bits /= 1000;
        i++;
      }
      return `${bits >= 100 || i === 0 ? bits.toFixed(0) : bits.toFixed(1)} ${units[i]}`;
    } else {
      let val = Number(bytesPerSec);
      const units = ["B/s", "KB/s", "MB/s", "GB/s"];
      let i = 0;
      while (val >= 1024 && i < units.length - 1) {
        val /= 1024;
        i++;
      }
      return `${val >= 100 || i === 0 ? val.toFixed(0) : val.toFixed(1)} ${units[i]}`;
    }
  }

  function formatLinkSpeed(bitsPerSec) {
    if (!bitsPerSec || bitsPerSec <= 0) return "Unknown";
    if (bitsPerSec >= 1e9) return `${(bitsPerSec / 1e9).toFixed(1)} Gbps`;
    if (bitsPerSec >= 1e6) return `${(bitsPerSec / 1e6).toFixed(0)} Mbps`;
    if (bitsPerSec >= 1e3) return `${(bitsPerSec / 1e3).toFixed(0)} Kbps`;
    return `${bitsPerSec} bps`;
  }

  // Theme & Settings Management
  function applySettingsToUI() {
    const validTheme = THEMES.find((t) => t.id === state.settings.theme) || THEMES[0];
    const theme = validTheme.id;
    htmlEl.setAttribute("data-theme", theme);
    if (quickThemeLabel) {
      quickThemeLabel.textContent = validTheme.label;
    }

    themeButtons.forEach((btn) => {
      btn.classList.toggle("active", btn.dataset.themeVal === theme);
    });
    unitButtons.forEach((btn) => {
      btn.classList.toggle("active", btn.dataset.unitVal === state.settings.speedUnit);
    });

    chkAlwaysOnTop.checked = !!state.settings.alwaysOnTop;
    if (chkStartOnBoot) chkStartOnBoot.checked = !!state.settings.startOnBoot;
    if (chkStartMinimized) chkStartMinimized.checked = !!state.settings.startMinimized;
    chkHideLoopback.checked = !!state.settings.hideLoopback;
    if (chkDataCapEnabled) chkDataCapEnabled.checked = !!state.settings.dataCapEnabled;
    if (inputDataCapGB) inputDataCapGB.value = state.settings.dataCapGB || 500;
    if (inputBillingDay) inputBillingDay.value = state.settings.billingDay || 1;
    if (inputSpikeMbps) inputSpikeMbps.value = state.settings.spikeAlertMbps || 0;
    if (chkNewAppAlert) chkNewAppAlert.checked = !!state.settings.newAppAlertEnabled;
    btnStayOnTop.classList.toggle("pinned", !!state.settings.alwaysOnTop);

    renderRealtimeView();
    if (state.activeTab === "history" && state.historyReport) {
      drawHistoryChart(state.historyReport);
      fetchQuotaStatus();
    }
  }

  async function loadSettings() {
    try {
      const res = await fetch("/api/settings");
      if (res.ok) {
        state.settings = await res.json();
        applySettingsToUI();
      }
    } catch (e) {
      console.error("Failed to load settings:", e);
    }
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
    } catch (e) {
      console.error("Failed to save settings:", e);
    }
  }

  // Tab Navigation
  navItems.forEach((btn) => {
    btn.addEventListener("click", () => {
      const tab = btn.dataset.tab;
      state.activeTab = tab;
      navItems.forEach((b) => b.classList.toggle("active", b === btn));
      tabPanels.forEach((p) => p.classList.toggle("active", p.id === `tab-${tab}`));

      if (tab === "realtime") {
        viewTitleEl.textContent = "Real-Time Bandwidth";
        renderRealtimeView();
      } else if (tab === "history") {
        viewTitleEl.textContent = "Historical Traffic Counter";
        loadHistoryInterfaces();
        fetchHistoryReport();
      } else if (tab === "settings") {
        viewTitleEl.textContent = "Settings & Windows Service";
        viewSubtitleEl.textContent = "Preferences & Daemon";
        fetchServiceStatus();
      }
    });
  });

  // Stay on Top Button & Checkbox
  btnStayOnTop.addEventListener("click", () => {
    const next = !state.settings.alwaysOnTop;
    saveSettings({ alwaysOnTop: next });
  });

  chkAlwaysOnTop.addEventListener("change", (e) => {
    saveSettings({ alwaysOnTop: e.target.checked });
  });

  if (chkStartOnBoot) {
    chkStartOnBoot.addEventListener("change", (e) => {
      saveSettings({ startOnBoot: e.target.checked });
    });
  }

  if (chkStartMinimized) {
    chkStartMinimized.addEventListener("change", (e) => {
      saveSettings({ startMinimized: e.target.checked });
    });
  }

  chkHideLoopback.addEventListener("change", (e) => {
    saveSettings({ hideLoopback: e.target.checked });
  });

  if (chkDataCapEnabled) {
    chkDataCapEnabled.addEventListener("change", (e) => {
      saveSettings({ dataCapEnabled: e.target.checked });
    });
  }

  if (inputDataCapGB) {
    inputDataCapGB.addEventListener("change", (e) => {
      const val = Math.max(1, parseFloat(e.target.value) || 500);
      saveSettings({ dataCapGB: val });
    });
  }

  if (inputBillingDay) {
    inputBillingDay.addEventListener("change", (e) => {
      const val = Math.min(28, Math.max(1, parseInt(e.target.value, 10) || 1));
      saveSettings({ billingDay: val });
    });
  }

  if (inputSpikeMbps) {
    inputSpikeMbps.addEventListener("change", (e) => {
      const val = Math.max(0, parseFloat(e.target.value) || 0);
      saveSettings({ spikeAlertMbps: val });
    });
  }

  if (chkNewAppAlert) {
    chkNewAppAlert.addEventListener("change", (e) => {
      saveSettings({ newAppAlertEnabled: e.target.checked });
    });
  }

  if (btnBackupDB) {
    btnBackupDB.addEventListener("click", () => {
      window.location.href = "/api/db/backup";
    });
  }

  if (btnResetDB) {
    btnResetDB.addEventListener("click", async () => {
      if (!confirm("Are you sure you want to clear all historical traffic counters?")) {
        return;
      }
      try {
        const res = await fetch("/api/db/reset", { method: "POST" });
        if (res.ok && dbActionStatus) {
          dbActionStatus.textContent = "All historical traffic counters have been reset.";
        }
      } catch (e) {
        console.error("Failed to reset DB:", e);
      }
    });
  }

  if (btnRunSpeedTest) {
    btnRunSpeedTest.addEventListener("click", async () => {
      btnRunSpeedTest.disabled = true;
      btnRunSpeedTest.textContent = "Testing...";
      if (stStatusText) {
        stStatusText.textContent = "Running download, upload, and loaded latency bufferbloat checks...";
      }
      try {
        const res = await fetch("/api/speedtest", { method: "POST" });
        if (res.ok) {
          const data = await res.json();
          if (stDownMbps) stDownMbps.textContent = `${(data.downloadMbps || 0).toFixed(1)} Mbps`;
          if (stUpMbps) stUpMbps.textContent = `${(data.uploadMbps || 0).toFixed(1)} Mbps`;
          if (stUnloadedPing) stUnloadedPing.textContent = `${(data.unloadedPingMs || 0).toFixed(1)} ms`;
          if (stLoadedPing) stLoadedPing.textContent = `${(data.loadedPingMs || 0).toFixed(1)} ms`;
          if (stJitter) stJitter.textContent = `${(data.jitterMs || 0).toFixed(1)} ms`;
          if (stGradeBadge) {
            const g = data.bufferbloatGrade || "—";
            stGradeBadge.textContent = g;
            stGradeBadge.classList.remove("grade-good", "grade-warn", "grade-bad");
            if (g === "A+" || g === "A") stGradeBadge.classList.add("grade-good");
            else if (g === "B") stGradeBadge.classList.add("grade-warn");
            else if (g === "C" || g === "D") stGradeBadge.classList.add("grade-bad");
          }
          if (stStatusText) {
            stStatusText.textContent = data.error
              ? `Error: ${data.error}`
              : `Completed at ${new Date().toLocaleTimeString()} via ${data.serverLabel || "Cloudflare Anycast"}`;
          }
        }
      } catch (e) {
        if (stStatusText) stStatusText.textContent = "Speed test request failed.";
      } finally {
        btnRunSpeedTest.disabled = false;
        btnRunSpeedTest.textContent = "Run Speed Test";
      }
    });
  }

  // Mini-Widget Floating Overlay Mode Toggle
  if (btnMiniWidget) {
    btnMiniWidget.addEventListener("click", async () => {
      if (!state.miniMode && window.outerWidth >= 450 && window.outerHeight >= 320) {
        state.normalBounds = {
          x: Math.max(0, Math.round(window.screenX)),
          y: Math.max(0, Math.round(window.screenY)),
          width: Math.round(window.outerWidth),
          height: Math.round(window.outerHeight),
        };
      }
      state.miniMode = !state.miniMode;
      document.body.classList.toggle("mini-mode", state.miniMode);
      btnMiniWidget.classList.toggle("pinned", state.miniMode);
      if (miniWidgetLabel) {
        miniWidgetLabel.textContent = state.miniMode ? "Full View" : "Mini Widget";
      }
      if (state.miniMode && state.activeTab !== "realtime") {
        // Switch to realtime tab for the mini widget
        const rtBtn = document.querySelector('.nav-item[data-tab="realtime"]');
        if (rtBtn) rtBtn.click();
      }
      try {
        await fetch("/api/window/mini", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ mini: state.miniMode }),
        });
      } catch (_) {}
      try {
        if (state.miniMode) {
          window.resizeTo(380, 230);
        } else if (state.normalBounds) {
          window.resizeTo(state.normalBounds.width, state.normalBounds.height);
          window.moveTo(state.normalBounds.x, state.normalBounds.y);
        }
      } catch (_) {}
      setTimeout(() => renderRealtimeView(), 120);
      setTimeout(() => renderRealtimeView(), 300);
    });
  }

  // Quick Theme Button & Segmented Controls
  btnQuickTheme.addEventListener("click", () => {
    const idx = THEMES.findIndex((t) => t.id === state.settings.theme);
    const nextIdx = (idx + 1) % THEMES.length;
    saveSettings({ theme: THEMES[nextIdx].id });
  });

  themeButtons.forEach((btn) => {
    btn.addEventListener("click", () => {
      saveSettings({ theme: btn.dataset.themeVal });
    });
  });

  unitButtons.forEach((btn) => {
    btn.addEventListener("click", () => {
      saveSettings({ speedUnit: btn.dataset.unitVal });
    });
  });

  // Sidebar Adapter List Rendering
  function renderSidebarAdapters(payload) {
    if (!payload) return;
    const ifaces = payload.interfaces || [];

    const items = [
      {
        id: "all",
        name: "All Network Interfaces",
        rxBps: payload.totalRxBps || 0,
        txBps: payload.totalTxBps || 0,
      },
      ...ifaces.map((nic) => ({
        id: nic.id,
        name: nic.name,
        rxBps: nic.currentRxBps || 0,
        txBps: nic.currentTxBps || 0,
      })),
    ];

    adapterListEl.innerHTML = items
      .map(
        (item) => `
      <button class="adapter-item ${state.selectedIface === item.id ? "active" : ""}" data-iface="${escapeHtml(item.id)}">
        <div class="adapter-item-top">
          <span class="adapter-name">${escapeHtml(item.name)}</span>
        </div>
        <div class="adapter-rates">
          <span>↓ ${formatRate(item.rxBps)}</span>
          <span>↑ ${formatRate(item.txBps)}</span>
        </div>
      </button>
    `
      )
      .join("");

    adapterListEl.querySelectorAll(".adapter-item").forEach((el) => {
      el.addEventListener("click", () => {
        state.selectedIface = el.dataset.iface;
        if (state.activeTab === "history") {
          state.historyFilter.iface = state.selectedIface;
          historyIfaceSelect.value = state.selectedIface;
          fetchHistoryReport();
        } else {
          renderRealtimeView();
          fetchConnections();
        }
        renderSidebarAdapters(state.latestRealtime);
      });
    });
  }

  // Real-Time View & Task Manager 60s Graph
  function renderRealtimeView() {
    const payload = state.latestRealtime;
    if (!payload) return;

    const ifaces = payload.interfaces || [];
    let selected = null;
    if (state.selectedIface !== "all") {
      selected = ifaces.find((x) => x.id === state.selectedIface) || null;
    }

    // Connectivity / Latency probes (1.1.1.1 & 8.8.8.8)
    if (rtPingBadgesEl) {
      const probes = payload.diagnostics?.targets || payload.diagnostics?.probes || [];
      if (probes.length === 0) {
        rtPingBadgesEl.textContent = "Probing...";
      } else {
        rtPingBadgesEl.innerHTML = probes
          .map((p) => {
            const addr = p.address || (p.target ? p.target.replace(":53", "") : "");
            const name = p.name || p.target || addr;
            if (!p.online) {
              return `<span class="ping-badge bad" title="${escapeHtml(name)} (${escapeHtml(addr)}) Offline">${escapeHtml(addr)} Offline</span>`;
            }
            const ms = p.latencyMs ? Number(p.latencyMs).toFixed(0) : "0";
            let cls = "good";
            if (p.latencyMs > 120 || p.lossPct > 10) cls = "bad";
            else if (p.latencyMs > 55 || p.lossPct > 0) cls = "warn";
            const lossTxt = p.lossPct > 0 ? ` (${p.lossPct.toFixed(0)}% loss)` : "";
            return `<span class="ping-badge ${cls}" title="${escapeHtml(name)}: ${ms} ms${lossTxt}">${escapeHtml(addr)}: ${ms}ms${lossTxt}</span>`;
          })
          .join("");
      }
    }

    if (!selected) {
      state.selectedIface = "all";
      viewSubtitleEl.textContent = "All Network Interfaces";
      rtAdapterDescEl.textContent = `Aggregated throughput across ${ifaces.length} active network adapter(s)`;

      const hist = payload.totalHistory || [];
      const lastPt = hist.length > 0 ? hist[hist.length - 1] : { rxPps: 0, txPps: 0 };

      let sumSessionRx = 0;
      let sumSessionTx = 0;
      ifaces.forEach((n) => {
        if (!n.isLoopback) {
          sumSessionRx += n.sessionRx || 0;
          sumSessionTx += n.sessionTx || 0;
        }
      });

      rtRxSpeedEl.textContent = formatRate(payload.totalRxBps);
      rtTxSpeedEl.textContent = formatRate(payload.totalTxBps);
      rtSessionRxEl.textContent = formatBytes(sumSessionRx);
      rtSessionTxEl.textContent = formatBytes(sumSessionTx);
      rtRxPpsEl.textContent = `${lastPt.rxPps || 0} pkt/s`;
      rtTxPpsEl.textContent = `${lastPt.txPps || 0} pkt/s`;
      rtLinkSpeedEl.textContent = `${ifaces.length} Adapters`;
      rtIPv4El.textContent = ifaces.find((i) => i.ipv4 && !i.isLoopback)?.ipv4 || "Multiple";
      rtMACEl.textContent = "All NICs";

      drawTaskManagerGraph(hist);
    } else {
      viewSubtitleEl.textContent = selected.name;
      rtAdapterDescEl.textContent = selected.description || selected.name;

      const hist = selected.history || [];
      const lastPt = hist.length > 0 ? hist[hist.length - 1] : { rxPps: 0, txPps: 0 };

      rtRxSpeedEl.textContent = formatRate(selected.currentRxBps);
      rtTxSpeedEl.textContent = formatRate(selected.currentTxBps);
      rtSessionRxEl.textContent = formatBytes(selected.sessionRx);
      rtSessionTxEl.textContent = formatBytes(selected.sessionTx);
      rtRxPpsEl.textContent = `${lastPt.rxPps || 0} pkt/s`;
      rtTxPpsEl.textContent = `${lastPt.txPps || 0} pkt/s`;
      rtLinkSpeedEl.textContent = formatLinkSpeed(selected.linkSpeedBits);
      rtIPv4El.textContent = selected.ipv4 || "—";
      rtMACEl.textContent = selected.mac || "—";

      drawTaskManagerGraph(hist);
    }
  }

  // Known Port / Service Resolver
  const KNOWN_PORTS = {
    20: "FTP-Data",
    21: "FTP",
    22: "SSH",
    23: "Telnet",
    25: "SMTP",
    53: "DNS",
    67: "DHCP",
    68: "DHCP",
    80: "HTTP",
    110: "POP3",
    123: "NTP",
    135: "RPC",
    137: "NetBIOS",
    138: "NetBIOS",
    139: "NetBIOS",
    143: "IMAP",
    443: "HTTPS",
    445: "SMB",
    465: "SMTPS",
    587: "SMTP",
    993: "IMAPS",
    995: "POP3S",
    1433: "MSSQL",
    1521: "Oracle",
    3306: "MySQL",
    3389: "RDP",
    5353: "mDNS",
    5432: "PostgreSQL",
    5672: "AMQP",
    6379: "Redis",
    8000: "HTTP-Alt",
    8080: "HTTP-Proxy",
    8443: "HTTPS-Alt",
    9876: "TraffCount",
    27017: "MongoDB",
  };

  function resolvePortService(localPort, remotePort, stateStr) {
    if (stateStr === "LISTEN" && KNOWN_PORTS[localPort]) {
      return KNOWN_PORTS[localPort];
    }
    if (remotePort && KNOWN_PORTS[remotePort]) {
      return KNOWN_PORTS[remotePort];
    }
    if (localPort && KNOWN_PORTS[localPort]) {
      return KNOWN_PORTS[localPort];
    }
    return "—";
  }

  // Live TCP/UDP Connections & Processes Inspector
  connSubTabBtns.forEach((btn) => {
    btn.addEventListener("click", () => {
      state.connFilter.subview = btn.dataset.subview;
      connSubTabBtns.forEach((b) => b.classList.toggle("active", b === btn));
      const isProc = state.connFilter.subview === "processes";
      connSocketsTable.classList.toggle("hidden", isProc);
      connProcessesTable.classList.toggle("hidden", !isProc);
      renderConnectionsTable();
    });
  });

  connStateBtns.forEach((btn) => {
    btn.addEventListener("click", () => {
      state.connFilter.state = btn.dataset.state;
      connStateBtns.forEach((b) => b.classList.toggle("active", b === btn));
      renderConnectionsTable();
    });
  });

  chkIncludeUDP.addEventListener("change", () => {
    state.connFilter.includeUDP = chkIncludeUDP.checked;
    connCountUdpWrapEl.style.display = chkIncludeUDP.checked ? "inline-flex" : "none";
    fetchConnections();
  });

  connSearchInput.addEventListener("input", () => {
    state.connFilter.search = connSearchInput.value.trim().toLowerCase();
    renderConnectionsTable();
  });

  async function fetchConnections() {
    if (state.activeTab !== "realtime") return;

    const params = new URLSearchParams();
    if (state.connFilter.includeUDP) {
      params.set("udp", "1");
    }
    if (state.selectedIface !== "all" && state.latestRealtime) {
      const sel = (state.latestRealtime.interfaces || []).find((i) => i.id === state.selectedIface);
      if (sel && sel.ipv4) {
        params.set("ip", sel.ipv4);
      }
    }

    try {
      const res = await fetch(`/api/connections?${params.toString()}`);
      if (!res.ok) return;
      state.connectionsSnap = await res.json();
      renderConnectionsTable();
    } catch (e) {
      console.error("Failed to fetch connections:", e);
    }
  }

  function renderConnectionsTable() {
    const snap = state.connectionsSnap;
    if (!snap) return;

    connCountEstEl.textContent = snap.establishedCount || 0;
    connCountListenEl.textContent = snap.listenCount || 0;
    connCountWaitEl.textContent = snap.timeWaitCount || 0;
    connCountUdpEl.textContent = snap.totalUdp || 0;

    const q = state.connFilter.search;

    if (state.connFilter.subview === "processes") {
      let procs = snap.topProcesses || [];
      if (q) {
        procs = procs.filter(
          (p) =>
            p.processName.toLowerCase().includes(q) ||
            String(p.pid).includes(q)
        );
      }
      if (procs.length === 0) {
        connProcTableBody.innerHTML = `<tr><td colspan="9" style="text-align:center;color:var(--text-muted);padding:18px;">No matching processes with active sockets.</td></tr>`;
        return;
      }
      connProcTableBody.innerHTML = procs
        .map((p) => {
          const initial = (p.processName || "?").charAt(0).toUpperCase();
          const totBytes = (p.totalRx || 0) + (p.totalTx || 0);
          const blockedBadge = p.blocked ? `<span class="fw-badge">BLOCKED</span>` : "";
          const blockBtnLabel = p.blocked ? "Unblock FW" : "Block FW";
          const blockBtnCls = p.blocked ? "tbl-btn active-block" : "tbl-btn";
          const canKill = p.pid > 4;
          return `
          <tr style="cursor:pointer" data-proc-filter="${escapeHtml(p.processName)}">
            <td>
              <div class="proc-cell">
                <span class="proc-badge">${escapeHtml(initial)}</span>
                <span title="${escapeHtml(p.exePath || p.processName)}">${escapeHtml(p.processName)}</span>
                ${blockedBadge}
              </div>
            </td>
            <td><code>${p.pid}</code></td>
            <td class="num" style="color:var(--rx-color)">${formatRate(p.rxBps || 0)}</td>
            <td class="num" style="color:var(--tx-color)">${formatRate(p.txBps || 0)}</td>
            <td class="num">${formatBytes(totBytes)}</td>
            <td class="num" style="color:var(--success);font-weight:600">${p.established}</td>
            <td class="num" style="color:var(--accent-primary)">${p.listening}</td>
            <td class="num"><strong>${p.total}</strong></td>
            <td class="num">
              <div class="tbl-actions">
                <button class="${blockBtnCls}" data-fw-proc="${escapeHtml(p.processName)}" data-fw-exe="${escapeHtml(p.exePath || "")}" data-fw-blocked="${p.blocked ? "1" : "0"}" title="Block or unblock application in Windows Defender Firewall">${blockBtnLabel}</button>
                ${canKill ? `<button class="tbl-btn danger" data-kill-pid="${p.pid}" data-kill-name="${escapeHtml(p.processName)}" title="Terminate process PID ${p.pid}">Kill</button>` : ""}
              </div>
            </td>
          </tr>
        `;
        })
        .join("");

      connProcTableBody.querySelectorAll("button[data-fw-proc]").forEach((btn) => {
        btn.addEventListener("click", async (e) => {
          e.stopPropagation();
          const processName = btn.dataset.fwProc;
          const exePath = btn.dataset.fwExe;
          const currentlyBlocked = btn.dataset.fwBlocked === "1";
          btn.disabled = true;
          try {
            const res = await fetch("/api/firewall/block", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({
                processName,
                exePath,
                blocked: !currentlyBlocked,
              }),
            });
            if (!res.ok) {
              const errData = await res.json().catch(() => ({}));
              alert(errData.error || "Failed to update Windows Firewall rule.");
            }
            fetchConnections();
          } catch (err) {
            alert("Failed to update firewall rule.");
          } finally {
            btn.disabled = false;
          }
        });
      });

      connProcTableBody.querySelectorAll("button[data-kill-pid]").forEach((btn) => {
        btn.addEventListener("click", async (e) => {
          e.stopPropagation();
          const pid = parseInt(btn.dataset.killPid, 10);
          const name = btn.dataset.killName;
          if (!confirm(`Terminate process ${name} (PID ${pid})?`)) return;
          btn.disabled = true;
          try {
            const res = await fetch("/api/connections/action", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ action: "kill_process", pid }),
            });
            if (!res.ok) {
              const errData = await res.json().catch(() => ({}));
              alert(errData.error || "Failed to terminate process.");
            }
            fetchConnections();
          } catch (_) {}
        });
      });

      connProcTableBody.querySelectorAll("tr[data-proc-filter]").forEach((tr) => {
        tr.addEventListener("click", () => {
          const procName = tr.dataset.procFilter;
          connSearchInput.value = procName;
          state.connFilter.search = procName.toLowerCase();
          state.connFilter.subview = "sockets";
          connSubTabBtns.forEach((b) => b.classList.toggle("active", b.dataset.subview === "sockets"));
          connSocketsTable.classList.remove("hidden");
          connProcessesTable.classList.add("hidden");
          renderConnectionsTable();
        });
      });
      return;
    }

    // Sockets List View
    let list = snap.connections || [];
    if (state.connFilter.state !== "ALL") {
      list = list.filter((c) => c.state === state.connFilter.state);
    }
    if (q) {
      list = list.filter(
        (c) =>
          (c.processName && c.processName.toLowerCase().includes(q)) ||
          (c.localAddr && c.localAddr.toLowerCase().includes(q)) ||
          (c.remoteAddr && c.remoteAddr.toLowerCase().includes(q)) ||
          (c.remoteHost && c.remoteHost.toLowerCase().includes(q)) ||
          (c.geoLabel && c.geoLabel.toLowerCase().includes(q)) ||
          (c.state && c.state.toLowerCase().includes(q)) ||
          String(c.pid).includes(q)
      );
    }

    const displayList = list.slice(0, 250);
    if (displayList.length === 0) {
      connTableBody.innerHTML = `<tr><td colspan="11" style="text-align:center;color:var(--text-muted);padding:18px;">No matching network connections.</td></tr>`;
      return;
    }

    connTableBody.innerHTML = displayList
      .map((c) => {
        const initial = (c.processName || "?").charAt(0).toUpperCase();
        const svc = resolvePortService(c.localPort, c.remotePort, c.state);
        let badgeCls = "";
        if (c.state === "ESTABLISHED") badgeCls = "est";
        else if (c.state === "LISTEN") badgeCls = "listen";
        else if (c.state === "TIME_WAIT" || c.state === "CLOSE_WAIT") badgeCls = "wait";

        const rttTxt = c.rttMs > 0 ? `${c.rttMs} ms` : "—";
        const trafficTxt =
          c.rxBytes > 0 || c.txBytes > 0
            ? `↓ ${formatBytes(c.rxBytes)} / ↑ ${formatBytes(c.txBytes)}`
            : "—";

        const geoBadge = c.countryCode ? `<span class="geo-badge">${escapeHtml(c.countryCode)}</span>` : "";
        const hostDisplay = c.remoteHost || c.geoLabel || "—";
        const hostTooltip = [c.remoteHost, c.geoLabel].filter(Boolean).join(" — ");
        const canCloseSocket = c.protocol === "TCP" && c.state === "ESTABLISHED";

        return `
        <tr>
          <td>
            <div class="proc-cell">
              <span class="proc-badge">${escapeHtml(initial)}</span>
              <span title="${escapeHtml(c.exePath || c.processName)}">${escapeHtml(c.processName)}</span>
            </div>
          </td>
          <td><code>${c.pid}</code></td>
          <td><code>${escapeHtml(c.protocol)}</code></td>
          <td><code>${escapeHtml(c.localAddr)}</code></td>
          <td><code>${escapeHtml(c.remoteAddr)}</code></td>
          <td>${geoBadge}<span class="host-tag" title="${escapeHtml(hostTooltip)}">${escapeHtml(hostDisplay)}</span></td>
          <td><span class="svc-tag">${escapeHtml(svc)}</span></td>
          <td class="num"><code>${rttTxt}</code></td>
          <td class="num" style="font-size:11.5px;color:var(--text-secondary)">${trafficTxt}</td>
          <td><span class="state-badge ${badgeCls}">${escapeHtml(c.state)}</span></td>
          <td class="num">
            ${
              canCloseSocket
                ? `<button class="tbl-btn danger" data-close-lip="${escapeHtml(c.localIP)}" data-close-lport="${c.localPort}" data-close-rip="${escapeHtml(c.remoteIP)}" data-close-rport="${c.remotePort}" title="Drop TCP connection">Close</button>`
                : "—"
            }
          </td>
        </tr>
      `;
      })
      .join("");

    connTableBody.querySelectorAll("button[data-close-lip]").forEach((btn) => {
      btn.addEventListener("click", async () => {
        btn.disabled = true;
        try {
          const res = await fetch("/api/connections/action", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              action: "close_socket",
              localIP: btn.dataset.closeLip,
              localPort: parseInt(btn.dataset.closeLport, 10),
              remoteIP: btn.dataset.closeRip,
              remotePort: parseInt(btn.dataset.closeRport, 10),
            }),
          });
          if (!res.ok) {
            const errData = await res.json().catch(() => ({}));
            alert(errData.error || "Failed to close TCP socket.");
          }
          fetchConnections();
        } catch (_) {}
      });
    });
  }

  function niceCeiling(maxBytesPerSec) {
    // Minimum scale ceiling of 12.5 KB/s (100 Kbps) like Windows Task Manager
    const floor = 12500;
    const val = Math.max(maxBytesPerSec * 1.15, floor);
    const pow = Math.pow(10, Math.floor(Math.log10(val)));
    const norm = val / pow;
    let step = 10;
    if (norm <= 1.25) step = 1.25;
    else if (norm <= 2.5) step = 2.5;
    else if (norm <= 5) step = 5;
    return step * pow;
  }

  function drawTaskManagerGraph(history) {
    if (!rtCanvas) return;
    const rect = rtCanvas.parentElement.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const width = Math.max(rect.width, 300);
    const height = Math.max(rect.height, 180);

    if (rtCanvas.width !== width * dpr || rtCanvas.height !== height * dpr) {
      rtCanvas.width = width * dpr;
      rtCanvas.height = height * dpr;
    }

    const ctx = rtCanvas.getContext("2d");
    ctx.save();
    ctx.scale(dpr, dpr);
    ctx.clearRect(0, 0, width, height);

    const style = getComputedStyle(htmlEl);
    const gridColor = style.getPropertyValue("--grid-line").trim();
    const rxColor = style.getPropertyValue("--rx-color").trim();
    const rxFill = style.getPropertyValue("--rx-fill").trim();
    const txColor = style.getPropertyValue("--tx-color").trim();
    const txFill = style.getPropertyValue("--tx-fill").trim();

    // Draw Task Manager style background grid
    ctx.strokeStyle = gridColor;
    ctx.lineWidth = 1;
    const rows = 4;
    const cols = 10;
    for (let r = 1; r < rows; r++) {
      const y = Math.round((height / rows) * r) + 0.5;
      ctx.beginPath();
      ctx.moveTo(0, y);
      ctx.lineTo(width, y);
      ctx.stroke();
    }
    for (let c = 1; c < cols; c++) {
      const x = Math.round((width / cols) * c) + 0.5;
      ctx.beginPath();
      ctx.moveTo(x, 0);
      ctx.lineTo(x, height);
      ctx.stroke();
    }

    const pts = history && history.length ? history : [];
    state.currentRtPoints = pts;
    let peak = 0;
    for (const p of pts) {
      if (p.rxBps > peak) peak = p.rxBps;
      if (p.txBps > peak) peak = p.txBps;
    }
    const maxScale = niceCeiling(peak);
    rtScaleMaxEl.textContent = formatRate(maxScale);

    if (pts.length < 2) {
      if (rtTooltip) rtTooltip.classList.add("hidden");
      ctx.restore();
      return;
    }

    const count = 60;
    const stepX = width / (count - 1);
    const offset = count - pts.length;

    function plotSeries(key, strokeColor, fillColor, dashed = false) {
      ctx.beginPath();
      for (let i = 0; i < pts.length; i++) {
        const x = (i + offset) * stepX;
        const ratio = Math.min((pts[i][key] || 0) / maxScale, 1);
        const y = height - ratio * (height - 10) - 2;
        if (i === 0) ctx.moveTo(x, y);
        else ctx.lineTo(x, y);
      }
      ctx.strokeStyle = strokeColor;
      ctx.lineWidth = 2.2;
      if (dashed) ctx.setLineDash([5, 4]);
      else ctx.setLineDash([]);
      ctx.stroke();

      // Area fill under curve
      ctx.setLineDash([]);
      const firstX = offset * stepX;
      ctx.lineTo(width, height);
      ctx.lineTo(firstX, height);
      ctx.closePath();
      ctx.fillStyle = fillColor;
      ctx.fill();
    }

    // Draw Outbound first, then Inbound on top
    plotSeries("txBps", txColor, txFill, false);
    plotSeries("rxBps", rxColor, rxFill, false);

    // Draw hover crosshair & update tooltip if mouse is over the real-time chart
    if (state.rtHover && pts.length > 0) {
      const rawIdx = Math.round(state.rtHover.x / stepX) - offset;
      const idx = Math.max(0, Math.min(pts.length - 1, rawIdx));
      const pt = pts[idx];
      const cx = (idx + offset) * stepX;
      const rxY = height - Math.min((pt.rxBps || 0) / maxScale, 1) * (height - 10) - 2;
      const txY = height - Math.min((pt.txBps || 0) / maxScale, 1) * (height - 10) - 2;

      // Vertical guide line
      ctx.strokeStyle = style.getPropertyValue("--border-strong").trim() || "rgba(255,255,255,0.3)";
      ctx.lineWidth = 1;
      ctx.setLineDash([4, 4]);
      ctx.beginPath();
      ctx.moveTo(cx, 0);
      ctx.lineTo(cx, height);
      ctx.stroke();
      ctx.setLineDash([]);

      // Highlight dots
      ctx.fillStyle = txColor;
      ctx.beginPath();
      ctx.arc(cx, txY, 4.5, 0, Math.PI * 2);
      ctx.fill();

      ctx.fillStyle = rxColor;
      ctx.beginPath();
      ctx.arc(cx, rxY, 4.5, 0, Math.PI * 2);
      ctx.fill();

      updateRealtimeTooltip(pt, pts.length - 1 - idx, cx, state.rtHover.y, width, height);
    } else if (rtTooltip) {
      rtTooltip.classList.add("hidden");
    }

    ctx.restore();
  }

  function updateRealtimeTooltip(pt, secsAgo, cx, mouseY, wrapW, wrapH) {
    if (!rtTooltip || !pt) return;
    const timeStr = pt.ts ? new Date(pt.ts * 1000).toLocaleTimeString() : "";
    const agoLabel = secsAgo === 0 ? "Now" : `${secsAgo}s ago`;
    const combinedRate = (pt.rxBps || 0) + (pt.txBps || 0);

    rtTooltip.innerHTML = `
      <div class="tt-header">
        <span>${escapeHtml(timeStr)}</span>
        <span class="tt-sub">${escapeHtml(agoLabel)}</span>
      </div>
      <div class="tt-row">
        <span class="tt-label"><i class="tt-dot rx"></i>Inbound</span>
        <strong style="color:var(--rx-color)">${formatRate(pt.rxBps)}</strong>
      </div>
      <div class="tt-row">
        <span class="tt-label"><i class="tt-dot tx"></i>Outbound</span>
        <strong style="color:var(--tx-color)">${formatRate(pt.txBps)}</strong>
      </div>
      <div class="tt-row">
        <span>Packets/s</span>
        <strong>↓ ${pt.rxPps || 0} / ↑ ${pt.txPps || 0}</strong>
      </div>
      <div class="tt-row tt-total">
        <span>Total Rate</span>
        <strong>${formatRate(combinedRate)}</strong>
      </div>
    `;
    rtTooltip.classList.remove("hidden");
    positionTooltip(rtTooltip, cx, mouseY, wrapW, wrapH);
  }

  function positionTooltip(el, targetX, targetY, wrapW, wrapH) {
    const ttW = el.offsetWidth || 210;
    const ttH = el.offsetHeight || 130;
    let left = targetX + 14;
    if (left + ttW + 10 > wrapW) {
      left = targetX - ttW - 14;
    }
    if (left < 8) left = 8;

    let top = targetY - ttH / 2;
    if (top < 8) top = 8;
    if (top + ttH + 8 > wrapH) top = Math.max(8, wrapH - ttH - 8);

    el.style.transform = `translate(${Math.round(left)}px, ${Math.round(top)}px)`;
  }

  // Historical Analytics View
  async function loadHistoryInterfaces() {
    try {
      const res = await fetch("/api/interfaces");
      if (!res.ok) return;
      const data = await res.json();
      const dbIfaces = data.interfaces || [];
      const currentVal = historyIfaceSelect.value || "all";

      historyIfaceSelect.innerHTML =
        `<option value="all">All Network Interfaces (Total)</option>` +
        dbIfaces
          .map(
            (i) =>
              `<option value="${escapeHtml(i.id)}">${escapeHtml(i.name)} ${
                i.description ? `(${escapeHtml(i.description)})` : ""
              }</option>`
          )
          .join("");

      if ([...historyIfaceSelect.options].some((o) => o.value === currentVal)) {
        historyIfaceSelect.value = currentVal;
      }
    } catch (e) {
      console.error("Failed to load history interfaces:", e);
    }
  }

  periodPills.forEach((btn) => {
    btn.addEventListener("click", () => {
      periodPills.forEach((b) => b.classList.toggle("active", b === btn));
      const p = btn.dataset.period || "";
      const d = btn.dataset.days ? parseInt(btn.dataset.days, 10) : 0;

      if (p === "custom") {
        customDateBox.classList.remove("hidden");
        return;
      }
      customDateBox.classList.add("hidden");
      state.historyFilter.period = p;
      state.historyFilter.days = d || 14;
      state.historyFilter.start = "";
      state.historyFilter.end = "";
      histPeriodLabelEl.textContent = btn.textContent;
      fetchHistoryReport();
    });
  });

  btnApplyDates.addEventListener("click", () => {
    if (!inputStartDate.value || !inputEndDate.value) return;
    const s = Math.floor(new Date(inputStartDate.value + "T00:00:00").getTime() / 1000);
    const e = Math.floor(new Date(inputEndDate.value + "T23:59:59").getTime() / 1000);
    if (s > e) return;
    state.historyFilter.period = "custom";
    state.historyFilter.start = String(s);
    state.historyFilter.end = String(e);
    histPeriodLabelEl.textContent = `${inputStartDate.value} → ${inputEndDate.value}`;
    fetchHistoryReport();
  });

  historyIfaceSelect.addEventListener("change", () => {
    state.historyFilter.iface = historyIfaceSelect.value;
    state.selectedIface = historyIfaceSelect.value;
    renderSidebarAdapters(state.latestRealtime);
    fetchHistoryReport();
  });

  function buildHistoryQueryParams() {
    const params = new URLSearchParams();
    params.set("iface", state.historyFilter.iface || "all");
    if (state.historyFilter.start && state.historyFilter.end) {
      params.set("start", state.historyFilter.start);
      params.set("end", state.historyFilter.end);
    } else if (state.historyFilter.period) {
      params.set("period", state.historyFilter.period);
    } else {
      params.set("days", String(state.historyFilter.days || 14));
    }
    return params;
  }

  function exportHistory(format) {
    const params = buildHistoryQueryParams();
    params.set("format", format);
    window.location.href = `/api/history/export?${params.toString()}`;
  }

  if (btnExportCSV) {
    btnExportCSV.addEventListener("click", () => exportHistory("csv"));
  }
  if (btnExportJSON) {
    btnExportJSON.addEventListener("click", () => exportHistory("json"));
  }

  async function fetchQuotaStatus() {
    if (!quotaBanner) return;
    try {
      const res = await fetch("/api/quota");
      if (!res.ok) return;
      const qs = await res.json();
      if (!qs.enabled) {
        quotaBanner.classList.add("hidden");
        return;
      }
      quotaBanner.classList.remove("hidden");
      const usedGB = ((qs.usedBytes || 0) / (1024 * 1024 * 1024)).toFixed(2);
      const capGB = (qs.capGb || 0).toFixed(0);
      const pct = Math.max(0, qs.usedPct || 0);
      const cycleStartStr = qs.cycleStart ? new Date(qs.cycleStart * 1000).toLocaleDateString() : "";

      if (quotaCycleText) {
        quotaCycleText.textContent = `Cycle started ${cycleStartStr} (Resets Day ${qs.billingDay || 1})`;
      }
      if (quotaUsedText) {
        quotaUsedText.textContent = `${usedGB} GB / ${capGB} GB`;
      }
      if (quotaPctBadge) {
        quotaPctBadge.textContent = `${pct.toFixed(1)}%`;
        quotaPctBadge.classList.remove("warn", "danger");
        if (qs.over100) quotaPctBadge.classList.add("danger");
        else if (qs.over80) quotaPctBadge.classList.add("warn");
      }
      if (quotaBarFill) {
        quotaBarFill.style.width = `${Math.min(pct, 100).toFixed(1)}%`;
        quotaBarFill.classList.remove("warn", "danger");
        if (qs.over100) quotaBarFill.classList.add("danger");
        else if (qs.over80) quotaBarFill.classList.add("warn");
      }
    } catch (_) {}
  }

  async function fetchHistoryReport() {
    const params = buildHistoryQueryParams();
    fetchQuotaStatus();

    try {
      const res = await fetch(`/api/history?${params.toString()}`);
      if (!res.ok) return;
      const report = await res.json();
      state.historyReport = report;
      renderHistoryReport(report);
    } catch (e) {
      console.error("Failed to fetch history report:", e);
    }
  }

  function renderHistoryReport(report) {
    histTotalRxEl.textContent = formatBytes(report.totalRxBytes);
    histTotalTxEl.textContent = formatBytes(report.totalTxBytes);
    histCombinedEl.textContent = formatBytes(report.combinedBytes);
    histAvgDailyEl.textContent = formatBytes(report.avgDailyBytes);
    histGranularityLabelEl.textContent =
      report.granularity === "hour"
        ? "Hourly traffic volume (Inbound vs. Outbound)"
        : "Daily traffic volume (Inbound vs. Outbound)";

    drawHistoryChart(report);

    const byIface = report.byInterface || [];
    if (byIface.length === 0) {
      historyIfaceTableBody.innerHTML = `<tr><td colspan="7" style="text-align:center;color:var(--text-muted);padding:18px;">No traffic recorded for this period yet.</td></tr>`;
    } else {
      historyIfaceTableBody.innerHTML = byIface
        .map(
          (item) => `
        <tr>
          <td><strong>${escapeHtml(item.name)}</strong></td>
          <td>${escapeHtml(item.description || "—")}</td>
          <td><code>${escapeHtml(item.mac || "—")}</code></td>
          <td class="num" style="color:var(--rx-color)">${formatBytes(item.rxBytes)}</td>
          <td class="num" style="color:var(--tx-color)">${formatBytes(item.txBytes)}</td>
          <td class="num"><strong>${formatBytes(item.totalBytes)}</strong></td>
          <td>
            <div class="share-bar-wrap">
              <div class="share-bar">
                <div class="share-bar-fill" style="width:${Math.min(item.sharePct || 0, 100).toFixed(1)}%"></div>
              </div>
              <span style="font-size:11.5px;min-width:40px;text-align:right">${(item.sharePct || 0).toFixed(1)}%</span>
            </div>
          </td>
        </tr>
      `
        )
        .join("");
    }

    if (historyProcTableBody) {
      const byProc = report.byProcess || [];
      if (byProc.length === 0) {
        historyProcTableBody.innerHTML = `<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:18px;">No per-application traffic recorded for this period yet.</td></tr>`;
      } else {
        historyProcTableBody.innerHTML = byProc
          .map((p) => {
            const initial = (p.processName || "?").charAt(0).toUpperCase();
            const blockedBadge = p.blocked ? `<span class="fw-badge">BLOCKED</span>` : "";
            return `
            <tr>
              <td>
                <div class="proc-cell">
                  <span class="proc-badge">${escapeHtml(initial)}</span>
                  <strong>${escapeHtml(p.processName)}</strong>
                  ${blockedBadge}
                </div>
              </td>
              <td><code style="font-size:11px;color:var(--text-muted)">${escapeHtml(p.exePath || "—")}</code></td>
              <td class="num" style="color:var(--rx-color)">${formatBytes(p.rxBytes)}</td>
              <td class="num" style="color:var(--tx-color)">${formatBytes(p.txBytes)}</td>
              <td class="num"><strong>${formatBytes(p.totalBytes)}</strong></td>
              <td>
                <div class="share-bar-wrap">
                  <div class="share-bar">
                    <div class="share-bar-fill" style="width:${Math.min(p.sharePct || 0, 100).toFixed(1)}%"></div>
                  </div>
                  <span style="font-size:11.5px;min-width:40px;text-align:right">${(p.sharePct || 0).toFixed(1)}%</span>
                </div>
              </td>
            </tr>
          `;
          })
          .join("");
      }
    }
  }

  function drawHistoryChart(report) {
    if (!histCanvas || !report) return;
    const rect = histCanvas.parentElement.getBoundingClientRect();
    const dpr = window.devicePixelRatio || 1;
    const width = Math.max(rect.width, 300);
    const height = Math.max(rect.height, 180);

    if (histCanvas.width !== width * dpr || histCanvas.height !== height * dpr) {
      histCanvas.width = width * dpr;
      histCanvas.height = height * dpr;
    }

    const ctx = histCanvas.getContext("2d");
    ctx.save();
    ctx.scale(dpr, dpr);
    ctx.clearRect(0, 0, width, height);

    const style = getComputedStyle(htmlEl);
    const gridColor = style.getPropertyValue("--grid-line").trim();
    const textColor = style.getPropertyValue("--text-muted").trim();
    const rxColor = style.getPropertyValue("--rx-color").trim();
    const txColor = style.getPropertyValue("--tx-color").trim();

    const padLeft = 64;
    const padRight = 18;
    const padTop = 16;
    const padBottom = 30;
    const plotW = width - padLeft - padRight;
    const plotH = height - padTop - padBottom;

    const series = report.series || [];
    let maxVal = 1024;
    for (const s of series) {
      if (s.rxBytes > maxVal) maxVal = s.rxBytes;
      if (s.txBytes > maxVal) maxVal = s.txBytes;
    }
    maxVal *= 1.15;

    // Horizontal grid lines + Y labels
    ctx.strokeStyle = gridColor;
    ctx.fillStyle = textColor;
    ctx.font = "11px Segoe UI, sans-serif";
    ctx.textAlign = "right";
    const steps = 4;
    for (let i = 0; i <= steps; i++) {
      const y = padTop + (plotH / steps) * i;
      const val = maxVal * (1 - i / steps);
      ctx.beginPath();
      ctx.moveTo(padLeft, y);
      ctx.lineTo(width - padRight, y);
      ctx.stroke();
      ctx.fillText(formatBytes(val), padLeft - 8, y + 4);
    }

    if (series.length === 0) {
      if (histTooltip) histTooltip.classList.add("hidden");
      ctx.restore();
      return;
    }

    const groupW = plotW / series.length;
    const barW = Math.max(Math.min(groupW * 0.34, 18), 2);
    const labelEvery = Math.max(1, Math.ceil(series.length / 10));

    let hoveredIdx = -1;
    if (state.histHover && state.histHover.x >= padLeft && state.histHover.x <= width - padRight) {
      hoveredIdx = Math.floor((state.histHover.x - padLeft) / groupW);
      if (hoveredIdx < 0 || hoveredIdx >= series.length) {
        hoveredIdx = -1;
      }
    }

    ctx.textAlign = "center";
    for (let i = 0; i < series.length; i++) {
      const pt = series[i];
      const groupX = padLeft + i * groupW;
      const cx = groupX + groupW / 2;

      if (i === hoveredIdx) {
        ctx.fillStyle = style.getPropertyValue("--bg-surface-hover").trim() || "rgba(255,255,255,0.06)";
        ctx.fillRect(groupX + 1, padTop, Math.max(groupW - 2, 2), plotH);
      }

      const rxH = Math.max((pt.rxBytes / maxVal) * plotH, pt.rxBytes > 0 ? 2 : 0);
      const txH = Math.max((pt.txBytes / maxVal) * plotH, pt.txBytes > 0 ? 2 : 0);

      // Rx Bar
      ctx.fillStyle = rxColor;
      ctx.fillRect(cx - barW - 1, padTop + plotH - rxH, barW, rxH);

      // Tx Bar
      ctx.fillStyle = txColor;
      ctx.fillRect(cx + 1, padTop + plotH - txH, barW, txH);

      if (i % labelEvery === 0 || i === series.length - 1) {
        ctx.fillStyle = textColor;
        ctx.fillText(pt.label, cx, height - 9);
      }
    }

    if (hoveredIdx >= 0) {
      const pt = series[hoveredIdx];
      const cx = padLeft + hoveredIdx * groupW + groupW / 2;
      updateHistoryTooltip(pt, report, cx, state.histHover.y, width, height);
    } else if (histTooltip) {
      histTooltip.classList.add("hidden");
    }

    ctx.restore();
  }

  function updateHistoryTooltip(pt, report, cx, mouseY, wrapW, wrapH) {
    if (!histTooltip || !pt) return;
    const total = (pt.rxBytes || 0) + (pt.txBytes || 0);
    const periodTotal = report.combinedBytes || 0;
    const sharePct = periodTotal > 0 ? ((total / periodTotal) * 100).toFixed(1) : "0.0";
    const bucketType = report.granularity === "hour" ? "Hourly Bucket" : "Daily Bucket";

    histTooltip.innerHTML = `
      <div class="tt-header">
        <span>${escapeHtml(pt.label)}</span>
        <span class="tt-sub">${bucketType}</span>
      </div>
      <div class="tt-row">
        <span class="tt-label"><i class="tt-dot rx"></i>Inbound</span>
        <strong style="color:var(--rx-color)">${formatBytes(pt.rxBytes)}</strong>
      </div>
      <div class="tt-row">
        <span class="tt-label"><i class="tt-dot tx"></i>Outbound</span>
        <strong style="color:var(--tx-color)">${formatBytes(pt.txBytes)}</strong>
      </div>
      <div class="tt-row tt-total">
        <span>Combined Total</span>
        <strong>${formatBytes(total)}</strong>
      </div>
      <div class="tt-row">
        <span>Period Share</span>
        <strong>${sharePct}%</strong>
      </div>
    `;
    histTooltip.classList.remove("hidden");
    positionTooltip(histTooltip, cx, mouseY, wrapW, wrapH);
  }

  // Attach interactive mouse hover listeners for both charts
  if (rtCanvas) {
    rtCanvas.addEventListener("mousemove", (e) => {
      const rect = rtCanvas.getBoundingClientRect();
      state.rtHover = { x: e.clientX - rect.left, y: e.clientY - rect.top };
      drawTaskManagerGraph(state.currentRtPoints);
    });
    rtCanvas.addEventListener("mouseleave", () => {
      state.rtHover = null;
      if (rtTooltip) rtTooltip.classList.add("hidden");
      drawTaskManagerGraph(state.currentRtPoints);
    });
  }

  if (histCanvas) {
    histCanvas.addEventListener("mousemove", (e) => {
      const rect = histCanvas.getBoundingClientRect();
      state.histHover = { x: e.clientX - rect.left, y: e.clientY - rect.top };
      if (state.historyReport) drawHistoryChart(state.historyReport);
    });
    histCanvas.addEventListener("mouseleave", () => {
      state.histHover = null;
      if (histTooltip) histTooltip.classList.add("hidden");
      if (state.historyReport) drawHistoryChart(state.historyReport);
    });
  }

  // Windows Service Control
  async function fetchServiceStatus() {
    try {
      const res = await fetch("/api/service");
      if (!res.ok) return;
      const st = await res.json();
      updateServiceUI(st);
    } catch (e) {
      console.error("Failed to query service status:", e);
    }
  }

  function updateServiceUI(st) {
    svcStateText.textContent = st.installed
      ? `TraffCountService is ${st.stateText}`
      : "TraffCountService is not installed (Running in standalone app mode)";
    svcBadge.textContent = st.stateText || "Unknown";
    svcBadge.classList.toggle("running", !!st.running);
  }

  async function invokeServiceAction(action) {
    const labelMap = {
      install: "Installing & Starting...",
      start: "Starting...",
      stop: "Stopping...",
      uninstall: "Uninstalling...",
    };
    if (svcStateText && labelMap[action]) {
      svcStateText.textContent = `TraffCountService: ${labelMap[action]}`;
    }
    if (svcBadge && labelMap[action]) {
      svcBadge.textContent = labelMap[action];
      svcBadge.classList.remove("running");
    }

    try {
      const res = await fetch("/api/service", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ action }),
      });
      if (res.ok) {
        const st = await res.json();
        updateServiceUI(st);
      }
    } catch (e) {
      // Expected if the server handed over port 9876 during start/stop transition
    }

    // Poll status frequently across the service/standalone transition so the UI updates immediately
    for (let i = 1; i <= 14; i++) {
      setTimeout(fetchServiceStatus, i * 350);
    }
  }

  document.getElementById("btnSvcInstall").addEventListener("click", () => invokeServiceAction("install"));
  document.getElementById("btnSvcStart").addEventListener("click", () => invokeServiceAction("start"));
  document.getElementById("btnSvcStop").addEventListener("click", () => invokeServiceAction("stop"));
  document.getElementById("btnSvcUninstall").addEventListener("click", () => invokeServiceAction("uninstall"));

  // WebSocket Live Stream Connection
  function connectWebSocket() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const ws = new WebSocket(`${proto}//${location.host}/ws`);
    const dot = document.querySelector("#wsStatus .status-dot");
    const txt = document.getElementById("wsStatusText");

    ws.onopen = () => {
      dot.classList.add("online");
      txt.textContent = "Live (1Hz)";
      if (state.activeTab === "settings") {
        fetchServiceStatus();
      }
    };

    ws.onmessage = (ev) => {
      try {
        const payload = JSON.parse(ev.data);
        state.latestRealtime = payload;
        renderSidebarAdapters(payload);
        if (state.activeTab === "realtime") {
          renderRealtimeView();
          fetchConnections();
        } else if (state.activeTab === "history") {
          fetchHistoryReport();
        } else if (state.activeTab === "settings") {
          fetchServiceStatus();
        }
      } catch (e) {
        console.error("WS parse error:", e);
      }
    };

    ws.onclose = () => {
      dot.classList.remove("online");
      txt.textContent = "Reconnecting...";
      setTimeout(connectWebSocket, 1500);
    };
  }

  function escapeHtml(str) {
    return String(str || "")
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  window.addEventListener("resize", () => {
    if (state.activeTab === "realtime" && state.latestRealtime) {
      renderRealtimeView();
    } else if (state.activeTab === "history" && state.historyReport) {
      drawHistoryChart(state.historyReport);
    }
    reportWindowBounds();
  });

  function reportWindowBounds() {
    if (
      state.miniMode ||
      window.outerWidth < 450 ||
      window.outerHeight < 320 ||
      window.screenX < -10000 ||
      window.screenY < -10000
    ) {
      return;
    }
    const payload = JSON.stringify({
      x: Math.round(window.screenX),
      y: Math.round(window.screenY),
      width: Math.round(window.outerWidth),
      height: Math.round(window.outerHeight),
      maximized: false,
      hasValue: true,
    });
    if (navigator.sendBeacon) {
      navigator.sendBeacon("/api/window/bounds", new Blob([payload], { type: "application/json" }));
    } else {
      fetch("/api/window/bounds", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: payload,
        keepalive: true,
      }).catch(() => {});
    }
  }

  window.addEventListener("beforeunload", reportWindowBounds);
  window.addEventListener("pagehide", reportWindowBounds);

  // Set default dates for custom picker
  const todayStr = new Date().toISOString().slice(0, 10);
  const twoWeeksAgoStr = new Date(Date.now() - 13 * 86400000).toISOString().slice(0, 10);
  inputStartDate.value = twoWeeksAgoStr;
  inputEndDate.value = todayStr;

  // Boot
  loadSettings().then(() => {
    connectWebSocket();
    fetchServiceStatus();
    fetchConnections();
    // Periodically sync settings in case Stay on Top or Start on Boot was toggled from the System Tray menu
    setInterval(async () => {
      try {
        const res = await fetch("/api/settings");
        if (res.ok) {
          const cfg = await res.json();
          if (cfg.theme && cfg.theme !== state.settings.theme) {
            state.settings.theme = cfg.theme;
            applySettingsToUI();
          }
          if (cfg.alwaysOnTop !== state.settings.alwaysOnTop) {
            state.settings.alwaysOnTop = cfg.alwaysOnTop;
            chkAlwaysOnTop.checked = !!cfg.alwaysOnTop;
            btnStayOnTop.classList.toggle("pinned", !!cfg.alwaysOnTop);
          }
          if (cfg.startOnBoot !== state.settings.startOnBoot) {
            state.settings.startOnBoot = cfg.startOnBoot;
            if (chkStartOnBoot) chkStartOnBoot.checked = !!cfg.startOnBoot;
          }
          if (cfg.startMinimized !== state.settings.startMinimized) {
            state.settings.startMinimized = cfg.startMinimized;
            if (chkStartMinimized) chkStartMinimized.checked = !!cfg.startMinimized;
          }
        }
      } catch (_) {}
    }, 2000);
  });
})();

