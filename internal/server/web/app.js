(() => {
  // Application State
  const state = {
    activeTab: "realtime",
    selectedIface: "all", // "all" or specific NIC id
    settings: {
      theme: "dark",
      alwaysOnTop: false,
      startOnBoot: false,
      speedUnit: "bits",
      hideLoopback: true,
      retentionDays: 365,
      refreshRateMs: 1000,
    },
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
  };

  // DOM Elements
  const htmlEl = document.documentElement;
  const navItems = document.querySelectorAll(".nav-item");
  const tabPanels = document.querySelectorAll(".tab-panel");
  const adapterListEl = document.getElementById("adapterList");
  const viewTitleEl = document.getElementById("viewTitle");
  const viewSubtitleEl = document.getElementById("viewSubtitle");
  const btnStayOnTop = document.getElementById("btnStayOnTop");
  const btnQuickTheme = document.getElementById("btnQuickTheme");

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
  const rtAdapterDescEl = document.getElementById("rtAdapterDesc");
  const rtScaleMaxEl = document.getElementById("rtScaleMax");
  const rtCanvas = document.getElementById("realtimeCanvas");

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
  const histTotalRxEl = document.getElementById("histTotalRx");
  const histTotalTxEl = document.getElementById("histTotalTx");
  const histCombinedEl = document.getElementById("histCombined");
  const histAvgDailyEl = document.getElementById("histAvgDaily");
  const histPeriodLabelEl = document.getElementById("histPeriodLabel");
  const histGranularityLabelEl = document.getElementById("histGranularityLabel");
  const historyIfaceTableBody = document.getElementById("historyIfaceTableBody");
  const histCanvas = document.getElementById("historyCanvas");

  // Settings DOM
  const themeButtons = document.querySelectorAll("#themeSelector .seg-btn");
  const unitButtons = document.querySelectorAll("#unitSelector .seg-btn");
  const chkAlwaysOnTop = document.getElementById("chkAlwaysOnTop");
  const chkStartOnBoot = document.getElementById("chkStartOnBoot");
  const chkHideLoopback = document.getElementById("chkHideLoopback");
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
    const theme = state.settings.theme === "light" ? "light" : "dark";
    htmlEl.setAttribute("data-theme", theme);

    themeButtons.forEach((btn) => {
      btn.classList.toggle("active", btn.dataset.themeVal === theme);
    });
    unitButtons.forEach((btn) => {
      btn.classList.toggle("active", btn.dataset.unitVal === state.settings.speedUnit);
    });

    chkAlwaysOnTop.checked = !!state.settings.alwaysOnTop;
    if (chkStartOnBoot) chkStartOnBoot.checked = !!state.settings.startOnBoot;
    chkHideLoopback.checked = !!state.settings.hideLoopback;
    btnStayOnTop.classList.toggle("pinned", !!state.settings.alwaysOnTop);

    renderRealtimeView();
    if (state.activeTab === "history" && state.historyReport) {
      drawHistoryChart(state.historyReport);
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

  chkHideLoopback.addEventListener("change", (e) => {
    saveSettings({ hideLoopback: e.target.checked });
  });

  // Quick Theme Button & Segmented Controls
  btnQuickTheme.addEventListener("click", () => {
    const next = state.settings.theme === "dark" ? "light" : "dark";
    saveSettings({ theme: next });
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
        connProcTableBody.innerHTML = `<tr><td colspan="6" style="text-align:center;color:var(--text-muted);padding:18px;">No matching processes with active sockets.</td></tr>`;
        return;
      }
      connProcTableBody.innerHTML = procs
        .map((p) => {
          const initial = (p.processName || "?").charAt(0).toUpperCase();
          return `
          <tr style="cursor:pointer" data-proc-filter="${escapeHtml(p.processName)}">
            <td>
              <div class="proc-cell">
                <span class="proc-badge">${escapeHtml(initial)}</span>
                <span>${escapeHtml(p.processName)}</span>
              </div>
            </td>
            <td><code>${p.pid}</code></td>
            <td class="num" style="color:var(--success);font-weight:600">${p.established}</td>
            <td class="num" style="color:var(--accent-primary)">${p.listening}</td>
            <td class="num">${p.other}</td>
            <td class="num"><strong>${p.total}</strong></td>
          </tr>
        `;
        })
        .join("");

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
          (c.state && c.state.toLowerCase().includes(q)) ||
          String(c.pid).includes(q)
      );
    }

    const displayList = list.slice(0, 250);
    if (displayList.length === 0) {
      connTableBody.innerHTML = `<tr><td colspan="7" style="text-align:center;color:var(--text-muted);padding:18px;">No matching network connections.</td></tr>`;
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

        return `
        <tr>
          <td>
            <div class="proc-cell">
              <span class="proc-badge">${escapeHtml(initial)}</span>
              <span>${escapeHtml(c.processName)}</span>
            </div>
          </td>
          <td><code>${c.pid}</code></td>
          <td><code>${escapeHtml(c.protocol)}</code></td>
          <td><code>${escapeHtml(c.localAddr)}</code></td>
          <td><code>${escapeHtml(c.remoteAddr)}</code></td>
          <td><span class="svc-tag">${escapeHtml(svc)}</span></td>
          <td><span class="state-badge ${badgeCls}">${escapeHtml(c.state)}</span></td>
        </tr>
      `;
      })
      .join("");
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
    let peak = 0;
    for (const p of pts) {
      if (p.rxBps > peak) peak = p.rxBps;
      if (p.txBps > peak) peak = p.txBps;
    }
    const maxScale = niceCeiling(peak);
    rtScaleMaxEl.textContent = formatRate(maxScale);

    if (pts.length < 2) {
      ctx.restore();
      return;
    }

    const count = 60;
    const stepX = width / (count - 1);

    function plotSeries(key, strokeColor, fillColor, dashed = false) {
      ctx.beginPath();
      const offset = count - pts.length;
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

    ctx.restore();
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

  async function fetchHistoryReport() {
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
      return;
    }

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
      ctx.restore();
      return;
    }

    const groupW = plotW / series.length;
    const barW = Math.max(Math.min(groupW * 0.34, 18), 2);
    const labelEvery = Math.max(1, Math.ceil(series.length / 10));

    ctx.textAlign = "center";
    for (let i = 0; i < series.length; i++) {
      const pt = series[i];
      const cx = padLeft + i * groupW + groupW / 2;

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

    ctx.restore();
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
      setTimeout(fetchServiceStatus, 1500);
    } catch (e) {
      console.error("Service action failed:", e);
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
    if (window.outerWidth < 400 || window.outerHeight < 300 || window.screenX < -10000 || window.screenY < -10000) {
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
          if (cfg.alwaysOnTop !== state.settings.alwaysOnTop) {
            state.settings.alwaysOnTop = cfg.alwaysOnTop;
            chkAlwaysOnTop.checked = !!cfg.alwaysOnTop;
            btnStayOnTop.classList.toggle("pinned", !!cfg.alwaysOnTop);
          }
          if (cfg.startOnBoot !== state.settings.startOnBoot) {
            state.settings.startOnBoot = cfg.startOnBoot;
            if (chkStartOnBoot) chkStartOnBoot.checked = !!cfg.startOnBoot;
          }
        }
      } catch (_) {}
    }, 2000);
  });
})();

