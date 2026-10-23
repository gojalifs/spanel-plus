// M1 skeleton UI: prove the embedded page can read the panel's own API.
// Real dashboard (projects, PHP versions, processes) arrives in M3.
async function loadStatus() {
  const line = document.getElementById('status-line');
  const detail = document.getElementById('status-detail');
  try {
    const res = await fetch('/api/v1/status');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const s = await res.json();
    line.textContent = 'Panel berjalan — versi ' + s.version + ' (' + s.commit + '), uptime ' + s.uptime_seconds + ' detik.';
    detail.innerHTML = '';
    const rows = [
      ['Port panel', s.panel_port],
      ['Apache home', s.apache_home],
      ['Service Apache', s.apache_service],
    ];
    for (const [k, v] of rows) {
      const dt = document.createElement('dt'); dt.textContent = k;
      const dd = document.createElement('dd'); dd.textContent = v;
      detail.append(dt, dd);
    }
  } catch (e) {
    line.textContent = 'Gagal membaca status panel: ' + e.message;
  }
}

async function loadDoctor() {
  const list = document.getElementById('doctor-list');
  try {
    const res = await fetch('/api/v1/doctor');
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const report = await res.json();
    list.innerHTML = '';
    for (const c of report.checks) {
      const li = document.createElement('li');
      li.className = c.status; // pass | warn | fail
      li.textContent = c.status.toUpperCase() + ' · ' + c.name + ' — ' + c.detail;
      list.appendChild(li);
    }
  } catch (e) {
    list.innerHTML = '';
    const li = document.createElement('li');
    li.className = 'fail';
    li.textContent = 'Gagal membaca doctor: ' + e.message;
    list.appendChild(li);
  }
}

loadStatus();
loadDoctor();
