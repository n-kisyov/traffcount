const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');

async function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms));
}

async function main() {
  const edgePath = 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe';
  const userDataDir = path.join(process.env.TEMP, 'traffcount_cdp_' + Date.now());
  const outDir = path.join(__dirname, '..', 'docs', 'screenshots');
  if (!fs.existsSync(outDir)) {
    fs.mkdirSync(outDir, { recursive: true });
  }

  console.log('Spawning Edge headless with CDP on port 9222...');
  const edge = spawn(edgePath, [
    '--headless=new',
    '--remote-debugging-port=9222',
    `--user-data-dir=${userDataDir}`,
    '--window-size=1280,840',
    '--disable-gpu',
    '--hide-scrollbars',
    'http://127.0.0.1:9876'
  ]);

  edge.on('error', err => console.error('Edge spawn error:', err));

  let targets = null;
  for (let i = 0; i < 20; i++) {
    await sleep(300);
    try {
      const res = await fetch('http://127.0.0.1:9222/json');
      if (res.ok) {
        targets = await res.json();
        break;
      }
    } catch (_) {}
  }

  if (!targets || targets.length === 0) {
    console.error('Failed to get CDP targets');
    edge.kill();
    process.exit(1);
  }

  const pageTarget = targets.find(t => t.type === 'page' && t.url.includes('127.0.0.1:9876')) || targets[0];
  const wsUrl = pageTarget.webSocketDebuggerUrl;
  console.log('Connecting to WebSocket:', wsUrl);

  const ws = new WebSocket(wsUrl);

  let msgId = 1;
  const pending = new Map();

  ws.onmessage = event => {
    const data = JSON.parse(event.data);
    if (data.id && pending.has(data.id)) {
      const { resolve, reject } = pending.get(data.id);
      pending.delete(data.id);
      if (data.error) reject(data.error);
      else resolve(data.result);
    }
  };

  await new Promise(resolve => ws.onopen = resolve);

  function send(method, params = {}) {
    const id = msgId++;
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', {
    width: 1280,
    height: 840,
    deviceScaleFactor: 1.5,
    mobile: false
  });

  console.log('Waiting for initial data to load and WebSocket to connect...');
  await sleep(2500);

  // Helper function to capture screenshot
  async function capture(filename) {
    const res = await send('Page.captureScreenshot', { format: 'png' });
    const fullPath = path.join(outDir, filename);
    fs.writeFileSync(fullPath, Buffer.from(res.data, 'base64'));
    console.log(`Saved: ${fullPath} (${(res.data.length * 0.75 / 1024).toFixed(0)} KB)`);
  }

  // Helper to evaluate JS in the page
  async function evalJs(expr) {
    const res = await send('Runtime.evaluate', { expression: `(() => { ${expr} })()`, returnByValue: true });
    if (res && res.exceptionDetails) {
      console.error('JS Evaluation Error:', res.exceptionDetails);
    }
    return res;
  }

  // 1. Real-Time Monitor (Top Overview)
  console.log('Capturing 01-realtime-monitor.png...');
  await evalJs(`
    document.querySelector('.nav-item[data-tab="realtime"]').click();
    const mc = document.querySelector('.main-content');
    if (mc) mc.scrollTop = 0;
  `);
  await sleep(1500);
  await capture('01-realtime-monitor.png');

  // 2. Traffic History Tab
  console.log('Capturing 02-traffic-history.png...');
  await evalJs(`
    document.querySelector('.nav-item[data-tab="history"]').click();
    const mc = document.querySelector('.main-content');
    if (mc) mc.scrollTop = 0;
  `);
  await sleep(2000);
  await capture('02-traffic-history.png');

  // 3. Active Connections & Sockets Inspector
  console.log('Capturing 03-active-connections.png...');
  await evalJs(`
    document.querySelector('.nav-item[data-tab="realtime"]').click();
    const tabRt = document.getElementById('tab-realtime');
    const scrollEl = document.querySelector('.conn-table-scroll');
    const conn = document.querySelector('.connections-card');
    if (tabRt) tabRt.style.paddingBottom = '450px';
    if (scrollEl) scrollEl.style.maxHeight = '480px';
    if (conn) conn.scrollIntoView({ behavior: 'instant', block: 'start' });
  `);
  await sleep(1500);
  await capture('03-active-connections.png');
  await evalJs(`
    const tabRt = document.getElementById('tab-realtime');
    const scrollEl = document.querySelector('.conn-table-scroll');
    if (tabRt) tabRt.style.paddingBottom = '';
    if (scrollEl) scrollEl.style.maxHeight = '';
  `);

  // 4. Speed Test & Diagnostics
  console.log('Capturing 04-speed-test.png...');
  await evalJs(`
    document.querySelector('.nav-item[data-tab="realtime"]').click();
    const stDown = document.getElementById("stDownMbps");
    const stUp = document.getElementById("stUpMbps");
    const stUnloaded = document.getElementById("stUnloadedPing");
    const stLoaded = document.getElementById("stLoadedPing");
    const stJitter = document.getElementById("stJitter");
    const stGrade = document.getElementById("stGradeBadge");
    const stStatus = document.getElementById("stStatusText");

    if (stDown) stDown.textContent = "101.2 Mbps";
    if (stUp) stUp.textContent = "39.6 Mbps";
    if (stUnloaded) stUnloaded.textContent = "15.1 ms";
    if (stLoaded) stLoaded.textContent = "35.0 ms";
    if (stJitter) stJitter.textContent = "6.9 ms";
    if (stGrade) {
      stGrade.textContent = "A";
      stGrade.className = "grade-badge grade-good";
    }
    if (stStatus) {
      stStatus.textContent = "Completed via Cloudflare Global Anycast (speed.cloudflare.com)";
    }
    const mc = document.querySelector('.main-content');
    const stCard = document.querySelector('.speedtest-card');
    if (mc && stCard) {
      mc.scrollTop = Math.max(0, mc.scrollTop + stCard.getBoundingClientRect().top - 200);
    }
  `);
  await sleep(1200);
  await capture('04-speed-test.png');

  // 5. Settings & Windows Service Management
  console.log('Capturing 05-settings-service.png...');
  await evalJs(`
    document.querySelector('.nav-item[data-tab="settings"]').click();
    const mc = document.querySelector('.main-content');
    if (mc) mc.scrollTop = 0;
  `);
  await sleep(1800);
  await capture('05-settings-service.png');

  // 6. Mini Widget Mode
  console.log('Capturing 06-mini-widget.png...');
  await evalJs(`
    document.querySelector('.nav-item[data-tab="realtime"]').click();
    document.body.classList.add('mini-mode');
    const miniBtn = document.getElementById('btnMiniWidget');
    if (miniBtn) miniBtn.classList.add('pinned');
    const lbl = document.getElementById('miniWidgetLabel');
    if (lbl) lbl.textContent = 'Full View';
  `);
  await send('Emulation.setDeviceMetricsOverride', {
    width: 400,
    height: 250,
    deviceScaleFactor: 2.0,
    mobile: false
  });
  await sleep(1200);
  await capture('06-mini-widget.png');

  // Reset
  await evalJs(`
    document.body.classList.remove('mini-mode');
  `);

  ws.close();
  edge.kill();
  try {
    fs.rmSync(userDataDir, { recursive: true, force: true });
  } catch (_) {}
  console.log('All screenshots captured successfully!');
}

main().catch(err => {
  console.error('Error:', err);
  process.exit(1);
});
