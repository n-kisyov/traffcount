const { spawn } = require('child_process');
const http = require('http');
const fs = require('fs');
const os = require('os');
const path = require('path');

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function findBrowserExecutable() {
  const candidates = [
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
    'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
    'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
  ];
  for (const p of candidates) {
    if (fs.existsSync(p)) return p;
  }
  throw new Error('Could not find Microsoft Edge or Google Chrome executable for headless CDP capture.');
}

async function pingApp(baseUrl, expectedApp) {
  try {
    const res = await fetch(`${baseUrl}/api/ping`, { signal: AbortSignal.timeout(1200) });
    if (!res.ok) return null;
    const data = await res.json();
    if (data && data.ok && (!expectedApp || data.app === expectedApp)) {
      return data;
    }
  } catch (_) {}
  return null;
}

async function waitForPing(baseUrl, expectedApp, timeoutMs = 10000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const p = await pingApp(baseUrl, expectedApp);
    if (p) return p;
    await sleep(250);
  }
  throw new Error(`Timed out waiting for ${expectedApp || 'server'} at ${baseUrl}`);
}

function startMockRaspberryPiAgent(port) {
  const startUnix = Math.floor(Date.now() / 1000);
  const hourNow = startUnix - (startUnix % 3600);
  const mockHist = Array.from({ length: 60 }, (_, idx) => {
    const ts = startUnix - (59 - idx);
    const wave = Math.sin(idx * 0.35) * 0.35 + 1.0;
    return {
      ts,
      rxBps: Math.round(680000 * wave),
      txBps: Math.round(145000 * wave),
      rxPps: 520,
      txPps: 190,
    };
  });

  const server = http.createServer((req, res) => {
    res.setHeader('Content-Type', 'application/json');
    const urlPath = req.url.split('?')[0];

    if (urlPath === '/api/ping') {
      res.end(JSON.stringify({
        ok: true,
        app: 'traffcount',
        version: '1.1.0',
        source: 'service',
        platform: 'linux',
        hostname: 'RaspberryPi-4-Gateway',
      }));
      return;
    }

    if (urlPath === '/api/realtime') {
      const now = Math.floor(Date.now() / 1000);
      const wave = 0.8 + (Math.sin(now * 0.7) + 1) * 0.35;
      const rxBps = Math.round(720000 * wave);
      const txBps = Math.round(165000 * wave);
      mockHist.push({ ts: now, rxBps, txBps, rxPps: 540, txPps: 210 });
      if (mockHist.length > 60) mockHist.shift();

      res.end(JSON.stringify({
        timestamp: now,
        source: 'service',
        totalRxBps: rxBps,
        totalTxBps: txBps,
        totalHistory: mockHist,
        interfaces: [
          {
            id: 'eth0_DCA632A1B2C3',
            name: 'eth0',
            description: 'Ethernet (eth0)',
            mac: 'DC:A6:32:A1:B2:C3',
            ipv4: '192.168.1.2',
            linkSpeedBits: 1000000000,
            isUp: true,
            isLoopback: false,
            currentRxBps: rxBps,
            currentTxBps: txBps,
            sessionRx: 485200120,
            sessionTx: 112840310,
            totalRx: 485200120,
            totalTx: 112840310,
            history: mockHist,
          },
        ],
        diagnostics: {
          probes: [
            { name: 'Default Gateway', target: '192.168.1.1', method: 'ICMP', latencyMs: 1, online: true, lossPct: 0 },
            { name: 'Cloudflare DNS', target: '1.1.1.1:53', method: 'TCP', latencyMs: 9, online: true, lossPct: 0 },
            { name: 'Google DNS', target: '8.8.8.8:53', method: 'TCP', latencyMs: 12, online: true, lossPct: 0 },
          ],
        },
      }));
      return;
    }

    if (urlPath === '/api/sync/export') {
      const traffic = [];
      const processes = [];
      for (let d = 0; d < 7; d++) {
        const bTs = hourNow - d * 86400;
        const factor = 1 + ((d * 3) % 5) * 0.22;
        traffic.push({
          ifaceId: 'eth0_DCA632A1B2C3',
          bucketTs: bTs,
          rxBytes: Math.round(1450000000 * factor),
          txBytes: Math.round(320000000 * factor),
        });
        processes.push({
          processName: 'pihole-FTL',
          exePath: '/usr/bin/pihole-FTL',
          bucketTs: bTs,
          rxBytes: Math.round(410000000 * factor),
          txBytes: Math.round(125000000 * factor),
          blocked: false,
        });
        processes.push({
          processName: 'wg-quick',
          exePath: '/usr/bin/wg',
          bucketTs: bTs,
          rxBytes: Math.round(820000000 * factor),
          txBytes: Math.round(180000000 * factor),
          blocked: false,
        });
      }
      res.end(JSON.stringify({
        sinceTs: hourNow - 7 * 86400,
        serverTs: Math.floor(Date.now() / 1000),
        interfaces: [
          { id: 'eth0_DCA632A1B2C3', name: 'eth0', description: 'Ethernet (eth0)', mac: 'DC:A6:32:A1:B2:C3', firstSeen: hourNow - 7 * 86400, lastSeen: Math.floor(Date.now() / 1000) },
        ],
        traffic,
        processes,
        speedTests: [
          {
            id: 1,
            timestamp: hourNow - 1800,
            unloadedPingMs: 9.4,
            jitterMs: 1.8,
            loadedPingMs: 14.2,
            downloadMbps: 482.5,
            uploadMbps: 94.3,
            bufferbloatGrade: 'A+',
            serverLabel: 'Cloudflare Global Anycast (speed.cloudflare.com)',
          },
        ],
      }));
      return;
    }

    if (urlPath === '/api/connections') {
      res.end(JSON.stringify({
        timestamp: Math.floor(Date.now() / 1000),
        topProcesses: [
          { pid: 742, processName: 'pihole-FTL', exePath: '/usr/bin/pihole-FTL', established: 4, rxBps: 98400, txBps: 31200, totalRx: 410200300, totalTx: 125100400 },
          { pid: 918, processName: 'sshd', exePath: '/usr/sbin/sshd', established: 1, rxBps: 12400, txBps: 8900, totalRx: 14200800, totalTx: 9800400 },
          { pid: 1104, processName: 'traffcount', exePath: '/usr/local/bin/traffcount', established: 2, rxBps: 4200, txBps: 16400, totalRx: 8920100, totalTx: 24100200 },
        ],
      }));
      return;
    }

    if (urlPath === '/api/quota') {
      res.end(JSON.stringify({
        enabled: true,
        cycleLabel: 'Cycle Day 1-30',
        usedGB: 142.8,
        capGB: 500,
        pctUsed: 28.56,
        projectedGB: 248.4,
        projectedPct: 49.68,
      }));
      return;
    }

    res.statusCode = 404;
    res.end(JSON.stringify({ error: 'not found' }));
  });

  return new Promise((resolve) => {
    server.listen(port, '127.0.0.1', () => resolve(server));
  });
}

async function createCDPClient(browserPath, initialUrl, userDataDir) {
  const cdpPort = 9229;
  console.log(`Spawning headless browser (${path.basename(browserPath)}) on CDP port ${cdpPort}...`);
  const browser = spawn(browserPath, [
    '--headless=new',
    `--remote-debugging-port=${cdpPort}`,
    `--user-data-dir=${userDataDir}`,
    '--window-size=1280,840',
    '--disable-gpu',
    '--hide-scrollbars',
    initialUrl,
  ]);

  let targets = null;
  for (let i = 0; i < 25; i++) {
    await sleep(250);
    try {
      const res = await fetch(`http://127.0.0.1:${cdpPort}/json`);
      if (res.ok) {
        const list = await res.json();
        if (Array.isArray(list) && list.length > 0) {
          targets = list;
          break;
        }
      }
    } catch (_) {}
  }

  if (!targets || targets.length === 0) {
    browser.kill();
    throw new Error('Failed to discover CDP page targets.');
  }

  const pageTarget = targets.find((t) => t.type === 'page') || targets[0];
  const ws = new WebSocket(pageTarget.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    ws.onopen = resolve;
    ws.onerror = reject;
  });

  let msgId = 1;
  const pending = new Map();
  ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    if (data.id && pending.has(data.id)) {
      const { resolve, reject } = pending.get(data.id);
      pending.delete(data.id);
      if (data.error) reject(new Error(data.error.message || JSON.stringify(data.error)));
      else resolve(data.result);
    }
  };

  function send(method, params = {}) {
    const id = msgId++;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }

  await send('Page.enable');
  await send('Runtime.enable');

  async function evalJs(expr) {
    const res = await send('Runtime.evaluate', {
      expression: `(() => { ${expr} })()`,
      returnByValue: true,
    });
    if (res && res.exceptionDetails) {
      const desc = res.exceptionDetails.exception?.description || res.exceptionDetails.text || 'JS evaluation failed';
      throw new Error(`CDP eval error: ${desc}`);
    }
    return res?.result?.value;
  }

  async function navigateAndWait(url, requiredSelector) {
    await send('Page.navigate', { url });
    for (let i = 0; i < 40; i++) {
      await sleep(200);
      try {
        const ok = await evalJs(`return document.readyState === 'complete' && !!document.querySelector(${JSON.stringify(requiredSelector)});`);
        if (ok) return;
      } catch (_) {}
    }
    const href = await evalJs('return location.href;').catch(() => 'unknown');
    throw new Error(`Timed out waiting for ${url} (landed on ${href}, missing ${requiredSelector})`);
  }

  async function setViewport(width, height, deviceScaleFactor) {
    await send('Emulation.setDeviceMetricsOverride', {
      width,
      height,
      deviceScaleFactor,
      mobile: false,
    });
  }

  async function capture(outDir, filename) {
    const res = await send('Page.captureScreenshot', { format: 'png' });
    const fullPath = path.join(outDir, filename);
    fs.writeFileSync(fullPath, Buffer.from(res.data, 'base64'));
    console.log(`Saved: ${fullPath} (${(Buffer.byteLength(res.data, 'base64') / 1024).toFixed(0)} KB)`);
  }

  return { browser, ws, evalJs, navigateAndWait, setViewport, capture };
}

async function main() {
  const rootDir = path.join(__dirname, '..');
  const outDir = path.join(rootDir, 'docs', 'screenshots');
  fs.mkdirSync(outDir, { recursive: true });

  const browserPath = findBrowserExecutable();
  const userDataDir = path.join(process.env.TEMP || os.tmpdir(), 'traffcount_cdp_' + Date.now());
  const tmpFiles = [];
  const spawnedChildren = [];
  let mockPiServer = null;
  let cdp = null;

  try {
    // 1. Locate or launch TraffCount Agent (prefer live running 127.0.0.1:9876)
    let agentUrl = 'http://127.0.0.1:9876';
    if (!(await pingApp(agentUrl, 'traffcount'))) {
      const exePath = path.join(rootDir, 'traffcount.exe');
      if (!fs.existsSync(exePath)) {
        throw new Error('traffcount.exe not found and no running TraffCount agent at http://127.0.0.1:9876. Build with .\\build.ps1 first.');
      }
      agentUrl = 'http://127.0.0.1:19876';
      const tmpAgentDb = path.join(process.env.TEMP || '.', `traffcount-cap-agent-${Date.now()}.db`);
      tmpFiles.push(tmpAgentDb, `${tmpAgentDb}-wal`, `${tmpAgentDb}-shm`);
      console.log(`No running agent on :9876 — starting temporary headless agent on ${agentUrl}...`);
      const child = spawn(exePath, ['--headless', '--addr', '127.0.0.1:19876'], {
        env: { ...process.env, TRAFFCOUNT_DB: tmpAgentDb },
        stdio: 'ignore',
      });
      spawnedChildren.push({ child, quitArgs: null, url: agentUrl });
      await waitForPing(agentUrl, 'traffcount');
    } else {
      console.log(`Using live TraffCount instance at ${agentUrl}`);
    }

    // Generate brief network traffic so real-time chart & connections have activity
    fetch('https://1.1.1.1/cdn-cgi/trace').catch(() => {});
    fetch(`${agentUrl}/api/connections`).catch(() => {});

    // 2. Locate or launch TraffCount Sync Server
    let syncUrl = 'http://127.0.0.1:9880';
    let syncHosts = [];
    if (await pingApp(syncUrl, 'traffcount-sync')) {
      const hRes = await fetch(`${syncUrl}/api/hosts`).catch(() => null);
      if (hRes && hRes.ok) syncHosts = await hRes.json();
    }
    if (!(await pingApp(syncUrl, 'traffcount-sync')) || syncHosts.length === 0) {
      const syncExe = path.join(rootDir, 'traffcount-sync.exe');
      if (!fs.existsSync(syncExe)) {
        throw new Error('traffcount-sync.exe not found. Build with .\\build-sync.ps1 first.');
      }
      syncUrl = 'http://127.0.0.1:19880';
      const tmpSyncDb = path.join(process.env.TEMP || '.', `traffcount-cap-sync-${Date.now()}.db`);
      tmpFiles.push(tmpSyncDb, `${tmpSyncDb}-wal`, `${tmpSyncDb}-shm`);
      console.log(`Starting temporary TraffCount Sync Server on ${syncUrl}...`);
      const syncChild = spawn(syncExe, ['--addr', '127.0.0.1:19880'], {
        env: { ...process.env, TRAFFCOUNT_SYNC_DB: tmpSyncDb },
        stdio: 'ignore',
      });
      spawnedChildren.push({ child: syncChild, quitArgs: ['quit', '--port', '19880'], exe: syncExe });
      await waitForPing(syncUrl, 'traffcount-sync');

      // Spin up mock Raspberry Pi 4 host + register both Windows 11 & Raspberry Pi hosts
      mockPiServer = await startMockRaspberryPiAgent(19877);
      await fetch(`${syncUrl}/api/hosts`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'Win11-Workstation', baseUrl: agentUrl, pollIntervalMs: 1000, tags: 'windows,desktop' }),
      });
      await fetch(`${syncUrl}/api/hosts`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'RaspberryPi-4-Gateway', baseUrl: 'http://127.0.0.1:19877', pollIntervalMs: 1000, tags: 'rpi4,router' }),
      });
    } else {
      console.log(`Using live TraffCount Sync Server at ${syncUrl} (${syncHosts.length} host(s))`);
    }

    // 3. Connect CDP browser and verify agent DOM is ready
    cdp = await createCDPClient(browserPath, agentUrl, userDataDir);
    await cdp.setViewport(1280, 840, 1.5);
    await cdp.navigateAndWait(agentUrl, '.nav-item[data-tab="realtime"]');
    await sleep(2500);

    // Screenshot 1: Real-Time Monitor
    console.log('Capturing 01-realtime-monitor.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="realtime"]').click();
      const mc = document.querySelector('.main-content');
      if (mc) mc.scrollTop = 0;
    `);
    await sleep(1200);
    await cdp.capture(outDir, '01-realtime-monitor.png');

    // Screenshot 2: Traffic History
    console.log('Capturing 02-traffic-history.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="history"]').click();
      const mc = document.querySelector('.main-content');
      if (mc) mc.scrollTop = 0;
    `);
    await sleep(1800);
    await cdp.capture(outDir, '02-traffic-history.png');

    // Screenshot 3: Active Connections & Process Inspector
    console.log('Capturing 03-active-connections.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="realtime"]').click();
      const tabRt = document.getElementById('tab-realtime');
      const scrollEl = document.querySelector('.conn-table-scroll');
      const conn = document.querySelector('.connections-card');
      if (tabRt) tabRt.style.paddingBottom = '450px';
      if (scrollEl) scrollEl.style.maxHeight = '480px';
      if (conn) conn.scrollIntoView({ behavior: 'instant', block: 'start' });
    `);
    await sleep(1500);
    await cdp.capture(outDir, '03-active-connections.png');
    await cdp.evalJs(`
      const tabRt = document.getElementById('tab-realtime');
      const scrollEl = document.querySelector('.conn-table-scroll');
      if (tabRt) tabRt.style.paddingBottom = '';
      if (scrollEl) scrollEl.style.maxHeight = '';
    `);

    // Screenshot 4: Speed Test & Bufferbloat Diagnostics
    console.log('Capturing 04-speed-test.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="realtime"]').click();
      const stDown = document.getElementById('stDownMbps');
      if (stDown && stDown.textContent.includes('—')) {
        stDown.textContent = '101.2 Mbps';
        const stUp = document.getElementById('stUpMbps');
        const stUnloaded = document.getElementById('stUnloadedPing');
        const stLoaded = document.getElementById('stLoadedPing');
        const stJitter = document.getElementById('stJitter');
        const stGrade = document.getElementById('stGradeBadge');
        const stStatus = document.getElementById('stStatusText');
        if (stUp) stUp.textContent = '39.6 Mbps';
        if (stUnloaded) stUnloaded.textContent = '15.1 ms';
        if (stLoaded) stLoaded.textContent = '35.0 ms';
        if (stJitter) stJitter.textContent = '6.9 ms';
        if (stGrade) { stGrade.textContent = 'A'; stGrade.className = 'grade-badge grade-good'; }
        if (stStatus) stStatus.textContent = 'Completed via Cloudflare Global Anycast (speed.cloudflare.com)';
      }
      const mc = document.querySelector('.main-content');
      const stCard = document.querySelector('.speedtest-card');
      if (mc && stCard) {
        mc.scrollTop = Math.max(0, mc.scrollTop + stCard.getBoundingClientRect().top - 160);
      }
    `);
    await sleep(1000);
    await cdp.capture(outDir, '04-speed-test.png');

    // Screenshot 5: Settings, Remote Sync Polling & Service Management
    console.log('Capturing 05-settings-service.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="settings"]').click();
      const mc = document.querySelector('.main-content');
      if (mc) mc.scrollTop = 0;
    `);
    await sleep(1500);
    await cdp.capture(outDir, '05-settings-service.png');

    // Screenshot 6: Mini Widget Mode
    console.log('Capturing 06-mini-widget.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="realtime"]').click();
      document.body.classList.add('mini-mode');
      const miniBtn = document.getElementById('btnMiniWidget');
      if (miniBtn) miniBtn.classList.add('pinned');
      const lbl = document.getElementById('miniWidgetLabel');
      if (lbl) lbl.textContent = 'Full View';
    `);
    await cdp.setViewport(400, 250, 2.0);
    await cdp.evalJs(`window.dispatchEvent(new Event('resize'));`);
    await sleep(1200);
    await cdp.capture(outDir, '06-mini-widget.png');

    // Reset viewport before switching to TraffCount Sync Server
    await cdp.evalJs(`document.body.classList.remove('mini-mode');`);
    await cdp.setViewport(1280, 840, 1.5);

    // Wait slightly more to accumulate several 1Hz live samples in Sync Server ring buffer
    await sleep(2500);
    await cdp.navigateAndWait(syncUrl, '.nav-item[data-tab="overview"]');
    await sleep(2500);

    // Screenshot 7: Sync Server — Fleet Overview
    console.log('Capturing 07-sync-fleet-overview.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="overview"]').click();
      const mc = document.querySelector('.main-content');
      if (mc) mc.scrollTop = 0;
    `);
    await sleep(1200);
    await cdp.capture(outDir, '07-sync-fleet-overview.png');

    // Screenshot 8: Sync Server — Host Explorer
    console.log('Capturing 08-sync-host-explorer.png...');
    await cdp.evalJs(`
      const sel = document.getElementById('explorerHostSelect');
      if (sel && sel.options.length > 1) {
        for (const opt of sel.options) {
          if (opt.textContent.includes('RaspberryPi')) {
            sel.value = opt.value;
            sel.dispatchEvent(new Event('change'));
            break;
          }
        }
      }
      document.querySelector('.nav-item[data-tab="explorer"]').click();
      const mc = document.querySelector('.main-content');
      if (mc) mc.scrollTop = 0;
    `);
    await sleep(1800);
    await cdp.capture(outDir, '08-sync-host-explorer.png');

    // Screenshot 9: Sync Server — Fleet History
    console.log('Capturing 09-sync-fleet-history.png...');
    await cdp.evalJs(`
      document.querySelector('.nav-item[data-tab="history"]').click();
      const mc = document.querySelector('.main-content');
      if (mc) mc.scrollTop = 0;
    `);
    await sleep(1800);
    await cdp.capture(outDir, '09-sync-fleet-history.png');

    console.log('All screenshots captured successfully!');
  } finally {
    if (cdp) {
      try { cdp.ws.close(); } catch (_) {}
      try { cdp.browser.kill(); } catch (_) {}
    }
    if (mockPiServer) {
      try { mockPiServer.close(); } catch (_) {}
    }
    for (const entry of spawnedChildren) {
      try {
        if (entry.quitArgs && entry.exe) {
          spawn(entry.exe, entry.quitArgs, { stdio: 'ignore' });
        }
      } catch (_) {}
      try { entry.child.kill(); } catch (_) {}
    }
    await sleep(300);
    try { fs.rmSync(userDataDir, { recursive: true, force: true }); } catch (_) {}
    for (const f of tmpFiles) {
      try { fs.rmSync(f, { force: true }); } catch (_) {}
    }
  }
}

main().catch((err) => {
  console.error('Capture failed:', err.message || err);
  process.exit(1);
});
