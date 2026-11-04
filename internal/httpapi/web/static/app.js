// SPanel+ dashboard (M3): projects CRUD, PHP version manager, config
// preview diff -> apply. All state changes go through /api/v1; the UI
// holds no logic beyond rendering and confirming.
'use strict';

const $ = (id) => document.getElementById(id);

// ---------- loading overlay ----------
// Shown only when a request takes longer than 150 ms, so fast local
// calls don't flicker. A counter keeps it up while several requests
// overlap (loadAll fires overview + projects + processes + doctor).
let loadingCount = 0;
let loadingTimer = null;

function setLoading(delta) {
  loadingCount = Math.max(0, loadingCount + delta);
  if (loadingCount > 0 && loadingTimer === null) {
    loadingTimer = setTimeout(() => {
      loadingTimer = null;
      if (loadingCount > 0) $('loading-overlay').hidden = false;
    }, 150);
  } else if (loadingCount === 0) {
    if (loadingTimer !== null) {
      clearTimeout(loadingTimer);
      loadingTimer = null;
    }
    $('loading-overlay').hidden = true;
  }
}

async function api(method, path, body) {
  setLoading(1);
  try {
    const opts = { method: method, headers: {} };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    const res = await fetch(path, opts);
    const data = await res.json().catch(() => ({}));
    if (res.status === 401) {
      showLogin();
      throw new Error(data.error || 'Sesi habis — login lagi.');
    }
    if (!res.ok) {
      throw new Error(data.error || ('HTTP ' + res.status));
    }
    return data;
  } finally {
    setLoading(-1);
  }
}

// ---------- sidebar navigation ----------

const views = ['overview', 'php', 'projects', 'project-form', 'processes', 'logs', 'tls', 'apache-config'];

function showView(name) {
  for (const v of views) {
    const sec = $('view-' + v);
    if (sec) sec.hidden = v !== name;
  }
  document.querySelectorAll('.nav-item').forEach((b) => {
    b.classList.toggle('active', b.dataset.view === name);
  });
  // On mobile the sidebar is off-canvas: close it after choosing.
  document.body.classList.remove('sidebar-open');
  if (name === 'logs') loadPanelLog();
  if (name === 'apache-config') loadApacheConfig();
}

// ---------- M5: login admin ----------

function showLogin() {
  $('login-card').hidden = false;
  $('dashboard').hidden = true;
  $('sidebar').hidden = true;
  $('btn-logout').hidden = true;
}

function showDashboard() {
  $('login-card').hidden = true;
  $('dashboard').hidden = false;
  $('sidebar').hidden = false;
  $('btn-logout').hidden = false;
}

// ---------- M5-AD: login toggle (akun panel vs akun AD) ----------

let loginMethod = 'local';

function setLoginMethod(method) {
  loginMethod = method;
  $('login-mode-local').className = method === 'local' ? 'active' : '';
  $('login-mode-ad').className = method === 'ad' ? 'active' : '';
  $('login-username').hidden = method !== 'ad';
  $('login-password').placeholder = method === 'ad' ? 'Password AD' : 'Password admin';
}

async function boot() {
  try {
    const st = await api('GET', '/api/v1/auth/status');
    if (!st.configured) {
      $('login-hint').textContent = 'Akun admin belum dibuat. Di server, jalankan: panel admin set-password — lalu login di sini.';
    }
    // The AD toggle is only offered when the panel actually has a
    // directory configured; otherwise it would promise a login
    // that can only fail.
    if (!st.ad_enabled) {
      $('login-mode-ad').disabled = true;
      $('login-mode-ad').title = 'Login AD belum diaktifkan di panel.json (ad_enabled).';
    }
    if (st.authenticated) {
      showDashboard();
      loadAll();
    } else {
      showLogin();
    }
  } catch (e) {
    showLogin();
  }
}

function toast(msg, ok) {
  const t = $('toast');
  t.textContent = msg;
  t.className = 'toast ' + (ok === false ? 'toast-err' : 'toast-ok');
  t.hidden = false;
  clearTimeout(toast._h);
  toast._h = setTimeout(() => { t.hidden = true; }, ok === false ? 12000 : 5000);
}

function el(tag, text, cls) {
  const n = document.createElement(tag);
  if (text !== undefined) n.textContent = text;
  if (cls) n.className = cls;
  return n;
}

function button(label, onClick) {
  const b = el('button', label);
  b.type = 'button';
  b.addEventListener('click', onClick);
  return b;
}

let registry = { projects: [], php_versions: [] };
let apachePort = 80; // site port from /api/v1/overview (httpd.conf Listen); 80 = historic default
let pendingSetPHP = null; // {name, version} awaiting apply from diff panel
let editingName = null;    // project name when the form is in edit mode

async function loadAll() {
  try {
    const ov = await api('GET', '/api/v1/overview');
    $('stat-projects').textContent = ov.projects_active + ' / ' + ov.projects_total;
    $('stat-php').textContent = String(ov.php_versions);
    $('stat-apache').textContent = ov.apache_status || '(tidak diketahui)';
    $('stat-apache-name').textContent = 'service: ' + (ov.apache_service || '-');
    $('stat-panel').textContent = 'v' + ov.status.version;
    $('apache-chip').textContent = 'Apache: ' + (ov.apache_status || '?');
    apachePort = (ov.status.apache_port || 80);
    registry = await api('GET', '/api/v1/projects');
    renderPHP();
    renderProjects();
    loadProcesses();
  } catch (e) {
    toast('Gagal memuat data: ' + e.message, false);
  }
  loadDoctor();
  loadTLS();
}

// ---------- TLS/HTTPS ----------

function tlsDaysLeft(iso) {
  if (!iso) return null;
  return (new Date(iso).getTime() - Date.now()) / 86400000;
}

function tlsWarnings(st) {
  const warns = [];
  if (st.renew_error) warns.push('Auto-renew gagal (' + st.renew_error + ') — periksa log panel, lalu coba Renew manual.');
  if (st.enabled) {
    const leaf = tlsDaysLeft(st.leaf_expiry);
    if (leaf !== null && leaf < 30) warns.push('Leaf TLS kedaluwarsa ' + (st.leaf_expiry || '').slice(0, 10) + ' — renew segera.');
    const ca = tlsDaysLeft(st.ca_expiry);
    if (ca !== null && ca < 365) warns.push('CA TLS kedaluwarsa ' + (st.ca_expiry || '').slice(0, 10) + ' — rencanakan reinit-ca (semua client install ulang).');
  }
  return warns;
}

async function loadTLS() {
  const banner = $('tls-banner');
  const status = $('tls-status');
  const warn = $('tls-overview-warn');
  try {
    const st = await api('GET', '/api/v1/tls/status');
    if (!st.enabled) {
      banner.textContent = 'TLS belum aktif — dashboard masih HTTP. Init di bawah untuk mengaktifkan HTTPS.';
      banner.className = 'muted';
      status.textContent = 'Apache HTTPS: ' + (st.tls_apache ? 'akan aktif di port ' + st.apache_port + ' sesudah init + apply' : 'nonaktif (tls_apache=false)') + '.';
      warn.hidden = true;
      return;
    }
    status.textContent = 'Aktif. CA sampai ' + (st.ca_expiry || '').slice(0, 10) +
      ' · leaf sampai ' + (st.leaf_expiry || '').slice(0, 10) +
      ' · fingerprint CA: ' + (st.ca_fingerprint || '') +
      ' · leaf IP: ' + (st.leaf_ips || []).join(', ') +
      (st.leaf_dns && st.leaf_dns.length ? ' · leaf DNS: ' + st.leaf_dns.join(', ') : '') +
      ' · Apache HTTPS: ' + (st.tls_apache ? 'port ' + st.apache_port : 'nonaktif');
    const warns = tlsWarnings(st);
    if (warns.length) {
      banner.textContent = warns.join(' ');
      banner.className = 'warn';
      warn.textContent = 'TLS: ' + warns.join(' ');
      warn.hidden = false;
    } else {
      banner.textContent = 'TLS sehat — tidak ada peringatan.';
      banner.className = 'muted';
      warn.hidden = true;
    }
  } catch (e) {
    banner.textContent = 'Gagal membaca status TLS: ' + e.message;
    banner.className = 'fail';
  }
}

function tlsSplitList(v) {
  return v.split(',').map((s) => s.trim()).filter((s) => s !== '');
}

async function tlsActionResult(res) {
  let msg = res.changed ? 'Leaf diperbarui sampai ' + (res.leaf_expiry || '').slice(0, 10) + '.' : 'Tidak ada perubahan (renew belum waktunya).';
  if (res.apache_restarted) msg += ' Apache di-restart.';
  else if (res.apache_error) msg += ' Apache TIDAK di-restart: ' + res.apache_error;
  $('tls-action-result').textContent = msg;
  await loadTLS();
}

async function downloadCA() {
  try {
    const res = await fetch('/api/v1/tls/ca.crt');
    if (res.status === 401) {
      showLogin();
      throw new Error('Sesi habis — login lagi.');
    }
    if (!res.ok) throw new Error('HTTP ' + res.status);
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'spanel-ca.crt';
    a.click();
    URL.revokeObjectURL(url);
    toast('ca.crt diunduh — install ke Trusted Root di client.');
  } catch (e) {
    toast(e.message, false);
  }
}

async function loadDoctor() {
  const list = $('doctor-list');
  try {
    const report = await api('GET', '/api/v1/doctor');
    list.innerHTML = '';
    for (const c of report.checks) {
      const li = el('li', c.status.toUpperCase() + ' · ' + c.name + ' — ' + c.detail, c.status);
      list.appendChild(li);
    }
  } catch (e) {
    list.innerHTML = '';
    list.appendChild(el('li', 'Gagal membaca doctor: ' + e.message, 'fail'));
  }
}

function renderPHP() {
  const tbody = $('php-table').querySelector('tbody');
  tbody.innerHTML = '';
  if (!registry.php_versions.length) {
    const tr = el('tr');
    const td = el('td', 'Belum ada versi PHP terdaftar.', 'muted');
    td.colSpan = 3;
    tr.appendChild(td);
    tbody.appendChild(tr);
  }
  for (const v of registry.php_versions) {
    const tr = el('tr');
    tr.append(el('td', v.version), el('td', v.path, 'mono'));
    const actions = el('td');
    actions.append(
      button('Extension', () => showModules(v.version)),
      button('Hapus', () => removePHP(v.version)),
    );
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }
  const sel = $('pf-php');
  sel.innerHTML = '';
  const none = el('option', '(tidak ada)');
  none.value = '';
  sel.appendChild(none);
  for (const v of registry.php_versions) {
    const o = el('option', v.version);
    o.value = v.version;
    sel.appendChild(o);
  }
}

let extState = null; // {version, initial: {name: enabled}} for the open checklist

function extStatusText(status) {
  switch (status) {
    case 'active': return 'aktif';
    case 'inactive': return 'nonaktif';
    case 'builtin': return 'bawaan';
    case 'broken': return 'rusak';
    case 'not_installed': return 'belum terinstal';
    default: return status;
  }
}

async function showModules(version) {
  try {
    const data = await api('GET', '/api/v1/php/' + encodeURIComponent(version) + '/extensions');
    $('php-modules-title').textContent = 'PHP ' + version;
    const list = $('php-ext-list');
    list.innerHTML = '';
    extState = { version: version, initial: {} };
    for (const e of data.extensions) {
      const row = el('div');
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.dataset.name = e.name;
      // Tercentang = aktif di php.ini ATAU sedang termuat (php -m).
      // Yang kedua penting buat extension bawaan (Core, date, pdo,
      // dsb.): mereka termuat tanpa baris ini, jadi tanpa ini mereka
      // tampil tidak tercentang padahal aktif. Extension rusak (baris
      // ini aktif tapi tidak termuat) tetap tercentang, sesuai
      // konfigurasinya.
      const enabled = e.ini_state === 'enabled' || e.loaded;
      cb.checked = enabled;
      extState.initial[e.name] = enabled;
      if (!e.toggleable) cb.disabled = true;
      row.appendChild(cb);
      row.appendChild(document.createTextNode(' ' + e.name + (e.zend ? ' (Zend)' : '') + ' — ' + extStatusText(e.status) + (e.hint ? ' · ' + e.hint : '')));
      list.appendChild(row);
    }
    $('php-ext-note').textContent = 'php.ini: ' + data.ini_path + ' · Sesudah simpan, restart Apache bila perlu agar semua proses php-cgi baru memakainya.';
    $('php-modules').hidden = false;
  } catch (e) {
    toast('Gagal membaca extension: ' + e.message, false);
  }
}

async function saveExtensions() {
  if (!extState) return;
  const changes = {};
  for (const cb of document.querySelectorAll('#php-ext-list input[type=checkbox]')) {
    const name = cb.dataset.name;
    if (cb.checked !== extState.initial[name]) changes[name] = cb.checked;
  }
  if (!Object.keys(changes).length) {
    toast('Tidak ada perubahan extension.');
    return;
  }
  try {
    const res = await api('POST', '/api/v1/php/' + encodeURIComponent(extState.version) + '/extensions', { extensions: changes });
    if (!res.changed) {
      toast('Tidak ada perubahan extension.');
    } else {
      let msg = 'Extension disimpan.';
      if (res.auto_enabled && res.auto_enabled.length) msg += ' Otomatis ikut aktif: ' + res.auto_enabled.join(', ') + '.';
      if (res.backup_path) msg += ' Backup: ' + res.backup_path;
      toast(msg);
    }
    showModules(extState.version);
  } catch (e) {
    toast(e.message, false);
  }
}

async function removePHP(version) {
  if (!confirm('Hapus pendaftaran PHP ' + version + '? Folder PHP-nya sendiri tidak dihapus.')) return;
  try {
    await api('DELETE', '/api/v1/php/' + encodeURIComponent(version));
    toast('PHP ' + version + ' dihapus dari registry.');
    loadAll();
  } catch (e) {
    toast(e.message, false);
  }
}

function phpSelect(current, onChange) {
  const sel = el('select');
  for (const v of registry.php_versions) {
    const o = el('option', v.version);
    o.value = v.version;
    if (v.version === current) o.selected = true;
    sel.appendChild(o);
  }
  sel.value = current;
  sel.addEventListener('change', () => onChange(sel.value));
  return sel;
}

function renderProjects() {
  const tbody = $('project-table').querySelector('tbody');
  tbody.innerHTML = '';
  if (!registry.projects.length) {
    const tr = el('tr');
    const td = el('td', 'Belum ada proyek. Tambahkan lewat form di bawah.', 'muted');
    td.colSpan = 7;
    tr.appendChild(td);
    tbody.appendChild(tr);
    return;
  }
  for (const p of registry.projects) {
    const tr = el('tr');
    tr.appendChild(el('td', p.name, 'mono'));
    const urlTd = el('td');
    const a = el('a', p.url_path, 'mono');
    a.href = 'http://' + window.location.hostname + (apachePort === 80 ? '' : ':' + apachePort) + p.url_path;
    a.target = '_blank';
    urlTd.appendChild(a);
    tr.appendChild(urlTd);
    tr.appendChild(el('td', p.type));
    const phpTd = el('td');
    if (p.type === 'static') {
      phpTd.textContent = p.php_version || '—';
    } else {
      phpTd.appendChild(phpSelect(p.php_version, (v) => previewSetPHP(p.name, v)));
    }
    tr.appendChild(phpTd);
    const enTd = el('td');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.checked = p.enabled;
    cb.addEventListener('change', () => setEnabled(p.name, cb.checked));
    enTd.appendChild(cb);
    tr.appendChild(enTd);
    const warnTd = el('td');
    warnTd.id = 'warn-' + p.name;
    warnTd.textContent = '…';
    tr.appendChild(warnTd);
    const actions = el('td');
    actions.append(
      button('Config', () => showConfig(p.name)),
      button('Edit', () => fillForm(p)),
      button('Hapus', () => removeProject(p.name)),
    );
    tr.appendChild(actions);
    tbody.appendChild(tr);
    loadWarnings(p.name);
  }
}

async function loadWarnings(name) {
  const cell = $('warn-' + name);
  if (!cell) return;
  try {
    const data = await api('GET', '/api/v1/projects/' + encodeURIComponent(name) + '/warnings');
    cell.innerHTML = '';
    if (!data.warnings.length) {
      cell.appendChild(el('span', '—', 'muted'));
      return;
    }
    for (const w of data.warnings) {
      const s = el('span', '⚠ ' + w.code, 'warn-badge');
      s.title = w.message;
      cell.appendChild(s);
    }
  } catch (e) {
    cell.textContent = '(gagal periksa)';
  }
}

async function setEnabled(name, enabled) {
  try {
    await api('POST', '/api/v1/projects/' + encodeURIComponent(name) + '/enabled', { enabled: enabled });
    toast('Proyek ' + name + (enabled ? ' diaktifkan' : ' dimatikan') + ' & di-apply.');
    loadAll();
  } catch (e) {
    toast(e.message, false);
    loadAll();
  }
}

async function previewSetPHP(name, version) {
  try {
    const diff = await api('POST', '/api/v1/projects/' + encodeURIComponent(name) + '/preview-set-php', { version: version });
    pendingSetPHP = { name: name, version: version };
    $('diff-title').textContent = name + ' → PHP ' + version;
    const pre = $('diff-body');
    pre.innerHTML = '';
    for (const line of diff.lines) {
      const prefix = line.kind === 'add' ? '+ ' : line.kind === 'remove' ? '- ' : '  ';
      const span = el('span', prefix + line.text + '\n', 'diff-' + line.kind);
      pre.appendChild(span);
    }
    $('diff-apply').hidden = false;
    $('diff-panel').hidden = false;
    $('diff-panel').scrollIntoView({ behavior: 'smooth', block: 'center' });
  } catch (e) {
    toast(e.message, false);
    loadAll();
  }
}

async function showConfig(name) {
  try {
    const data = await api('GET', '/api/v1/projects/' + encodeURIComponent(name) + '/config');
    pendingSetPHP = null;
    $('diff-title').textContent = 'config aktif ' + name;
    const pre = $('diff-body');
    pre.innerHTML = '';
    pre.appendChild(el('span', data.config, 'diff-context'));
    $('diff-apply').hidden = true;
    $('diff-panel').hidden = false;
  } catch (e) {
    toast(e.message, false);
  }
}

async function removeProject(name) {
  if (!confirm('Hapus proyek ' + name + ' dari panel? Folder proyek tidak dihapus; config-nya dicabut dari Apache lewat apply.')) return;
  try {
    await api('DELETE', '/api/v1/projects/' + encodeURIComponent(name));
    toast('Proyek ' + name + ' dihapus & di-apply.');
    loadAll();
  } catch (e) {
    toast(e.message, false);
  }
}

function fillForm(p) {
  editingName = p.name;
  showView('project-form');
  $('project-form-title').textContent = 'Edit proyek — ' + p.name;
  $('pf-submit').textContent = 'Simpan & apply';
  $('pf-name').value = p.name;
  $('pf-name').disabled = true;
  $('pf-url').value = p.url_path;
  $('pf-path').value = p.fs_path;
  $('pf-type').value = p.type;
  $('pf-public').value = p.public_subdir || '';
  $('pf-php').value = p.php_version || '';
  $('pf-enabled').checked = p.enabled;
  $('pf-processes').innerHTML = '';
  for (const def of (p.cli_processes || [])) addProcessRow(def);
  $('project-form').scrollIntoView({ behavior: 'smooth', block: 'center' });
}

function resetForm() {
  editingName = null;
  $('project-form-title').textContent = 'Tambah proyek';
  $('pf-submit').textContent = 'Tambah & apply';
  $('pf-name').disabled = false;
  $('project-form').reset();
  $('pf-enabled').checked = true;
  $('pf-processes').innerHTML = '';
}

document.addEventListener('DOMContentLoaded', () => {
  document.querySelectorAll('.nav-item').forEach((b) => {
    b.addEventListener('click', () => showView(b.dataset.view));
  });
  $('btn-sidebar-toggle').addEventListener('click', () => {
    document.body.classList.toggle('sidebar-open');
  });
  $('btn-panel-log-reload').addEventListener('click', loadPanelLog);
  $('fcgid-form').addEventListener('submit', saveFcgid);
  $('apache-modules-save').addEventListener('click', saveApacheModules);
  $('apache-config-file').addEventListener('change', loadApacheConfigFile);
  $('apache-config-load').addEventListener('click', loadApacheConfigFile);
  $('apache-config-save').addEventListener('click', saveApacheConfigFile);

  $('tls-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    try {
      const info = await api('POST', '/api/v1/tls/init', {
        ips: tlsSplitList($('tls-ips').value),
        names: tlsSplitList($('tls-names').value),
        ca_years: parseInt($('tls-ca-years').value, 10) || 0,
      });
      toast('TLS di-init. Restart service panel agar dashboard jadi HTTPS, lalu install ca.crt di client.' + (info.warning ? ' ' + info.warning : ''));
      await loadTLS();
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('tls-regenerate').addEventListener('click', async () => {
    try {
      const res = await api('POST', '/api/v1/tls/regenerate-leaf', {
        ips: tlsSplitList($('tls-ips').value),
        names: tlsSplitList($('tls-names').value),
      });
      toast('Leaf baru diterbitkan.');
      await tlsActionResult(res);
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('tls-renew').addEventListener('click', async () => {
    try {
      const res = await api('POST', '/api/v1/tls/renew', {});
      await tlsActionResult(res);
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('tls-download-ca').addEventListener('click', downloadCA);
  $('tls-reinit').addEventListener('click', async () => {
    if (!$('tls-reinit-confirm').checked) {
      toast('Centang dulu pernyataan reinit — ini mengganti trust anchor.', false);
      return;
    }
    if (!window.confirm('Reinit CA mengganti trust anchor. Semua client harus install ulang ca.crt. Lanjutkan?')) return;
    try {
      const res = await api('POST', '/api/v1/tls/reinit-ca', {
        confirm: true,
        ips: tlsSplitList($('tls-ips').value),
        names: tlsSplitList($('tls-names').value),
        ca_years: parseInt($('tls-ca-years').value, 10) || 0,
      });
      toast('CA diganti — semua client HARUS install ulang ca.crt yang baru.');
      await tlsActionResult(res);
    } catch (e) {
      toast(e.message, false);
    }
  });

  $('php-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    try {
      const reg = await api('POST', '/api/v1/php', { path: $('php-path').value });
      toast('PHP ' + reg.version.version + ' (' + reg.full_version + ', ' + (reg.nts ? 'NTS' : 'TS') + ' ' + reg.arch + ') terdaftar; php.ini: ' + reg.php_ini + '.');
      $('php-path').value = '';
      loadAll();
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('php-ext-save').addEventListener('click', saveExtensions);
  $('php-scan').addEventListener('click', async () => {
    try {
      const res = await api('POST', '/api/v1/php/scan', {});
      let msg = 'Pindai ' + res.root + ': ';
      if (!res.registered?.length && !res.skipped?.length) {
        msg += 'tidak ada PHP baru.';
      } else {
        if (res.registered?.length) msg += 'terdaftar: ' + res.registered.join(', ') + '. ';
        if (res.skipped?.length) msg += 'dilewati: ' + res.skipped.map(s => s.path + ' (' + s.reason + ')').join('; ') + '.';
      }
      toast(msg);
      loadAll();
    } catch (e) {
      toast(e.message, false);
    }
  });

  $('project-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const payload = {
      name: $('pf-name').value,
      url_path: $('pf-url').value,
      fs_path: $('pf-path').value,
      type: $('pf-type').value,
      public_subdir: $('pf-public').value,
      php_version: $('pf-php').value,
      enabled: $('pf-enabled').checked,
      cli_processes: collectProcesses(),
    };
    try {
      if (editingName) {
        await api('PUT', '/api/v1/projects/' + encodeURIComponent(editingName), payload);
        toast('Proyek ' + editingName + ' diperbarui & di-apply.');
      } else {
        await api('POST', '/api/v1/projects', payload);
        toast('Proyek ' + payload.name + ' ditambah & di-apply.');
      }
      resetForm();
      showView('projects');
      loadAll();
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('pf-reset').addEventListener('click', resetForm);

  $('diff-apply').addEventListener('click', async () => {
    if (!pendingSetPHP) return;
    try {
      await api('POST', '/api/v1/projects/' + encodeURIComponent(pendingSetPHP.name) + '/set-php', { version: pendingSetPHP.version });
      toast('PHP ' + pendingSetPHP.name + ' → ' + pendingSetPHP.version + ' ter-apply.');
      pendingSetPHP = null;
      $('diff-panel').hidden = true;
      loadAll();
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('diff-cancel').addEventListener('click', () => {
    pendingSetPHP = null;
    $('diff-panel').hidden = true;
    loadAll();
  });

  $('pf-proc-add').addEventListener('click', () => addProcessRow());
  $('pf-proc-queue').addEventListener('click', () => addProcessRow({ name: 'queue', command: 'artisan queue:work', kind: '', autostart: true, restart: 'on-failure' }));
  $('pf-proc-sched').addEventListener('click', () => addProcessRow({ name: 'scheduler', command: '', kind: 'schedule:run', autostart: true }));
  $('log-close').addEventListener('click', () => { $('log-panel').hidden = true; });
  $('run-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    const out = $('run-output');
    out.hidden = false;
    out.textContent = 'Menjalankan…';
    try {
      const res = await api('POST', '/api/v1/projects/' + encodeURIComponent($('run-project').value) + '/run', { command: $('run-command').value });
      out.textContent = '[exit ' + res.exit_code + ']\n' + (res.output || '(tanpa output)');
    } catch (e) {
      out.textContent = 'Gagal: ' + e.message;
    }
  });

  $('btn-apache-restart').addEventListener('click', async () => {
    if (!confirm('Restart service Apache sekarang? Semua proyek berhenti sesaat.')) return;
    try {
      await api('POST', '/api/v1/apache/restart');
      toast('Apache di-restart.');
      loadAll();
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('btn-apache-test').addEventListener('click', async () => {
    try {
      await api('POST', '/api/v1/apache/test');
      toast('httpd -t: config valid.');
    } catch (e) {
      toast(e.message, false);
    }
  });

  $('login-mode-local').addEventListener('click', () => setLoginMethod('local'));
  $('login-mode-ad').addEventListener('click', () => setLoginMethod('ad'));
  $('login-form').addEventListener('submit', async (ev) => {
    ev.preventDefault();
    try {
      const body = { method: loginMethod, password: $('login-password').value };
      if (loginMethod === 'ad') body.username = $('login-username').value;
      await api('POST', '/api/v1/auth/login', body);
      $('login-password').value = '';
      $('login-username').value = '';
      showDashboard();
      loadAll();
    } catch (e) {
      toast(e.message, false);
    }
  });
  $('btn-logout').addEventListener('click', async () => {
    try { await api('POST', '/api/v1/auth/logout'); } catch (e) { /* sesi sudah mati */ }
    showLogin();
  });

  boot();
});

// ---------- M4: supervisor processes ----------

function renderRunProjects() {
  const sel = $('run-project');
  sel.innerHTML = '';
  for (const p of registry.projects) {
    const o = el('option', p.name);
    o.value = p.name;
    sel.appendChild(o);
  }
}

async function loadProcesses() {
  renderRunProjects();
  const tbody = $('process-table').querySelector('tbody');
  try {
    const data = await api('GET', '/api/v1/processes');
    tbody.innerHTML = '';
    if (!data.processes.length) {
      const tr = el('tr');
      const td = el('td', 'Belum ada definisi proses. Tambahkan lewat Edit proyek → Proses CLI (ada template Laravel).', 'muted');
      td.colSpan = 7;
      tr.appendChild(td);
      tbody.appendChild(tr);
      return;
    }
    for (const st of data.processes) {
      const tr = el('tr');
      tr.appendChild(el('td', st.project, 'mono'));
      tr.appendChild(el('td', st.name, 'mono'));
      tr.appendChild(el('td', st.kind === 'schedule:run' ? 'artisan schedule:run (tiap menit)' : (st.command || ''), 'mono'));
      const stateTd = el('td');
      const chip = el('span', st.state, 'state-chip state-' + st.state);
      stateTd.appendChild(chip);
      if (st.kind === 'schedule:run' && st.last_run_at && !st.last_run_at.startsWith('0001')) {
        stateTd.appendChild(el('div', 'terakhir: ' + st.last_run_at, 'muted'));
      }
      tr.appendChild(stateTd);
      tr.appendChild(el('td', st.pid ? String(st.pid) : '—'));
      tr.appendChild(el('td', String(st.restarts || 0)));
      const actions = el('td');
      if (st.kind === 'schedule:run') {
        actions.append(
          button('Jalankan sekarang', () => scheduleRunNow(st.project)),
          button('Log', () => showProcessLog(st.project, 'scheduler')),
        );
      } else {
        actions.append(
          button('Start', () => processAction(st.project, st.name, 'start')),
          button('Stop', () => processAction(st.project, st.name, 'stop')),
          button('Restart', () => processAction(st.project, st.name, 'restart')),
          button('Log', () => showProcessLog(st.project, st.name)),
        );
      }
      tr.appendChild(actions);
      tbody.appendChild(tr);
    }
  } catch (e) {
    tbody.innerHTML = '';
    const tr = el('tr');
    const td = el('td', 'Gagal memuat proses: ' + e.message, 'fail');
    td.colSpan = 7;
    tr.appendChild(td);
    tbody.appendChild(tr);
  }
}

async function processAction(project, name, action) {
  try {
    await api('POST', '/api/v1/projects/' + encodeURIComponent(project) + '/processes/' + encodeURIComponent(name) + '/' + action);
    toast('Proses ' + project + '/' + name + ': ' + action + ' ok.');
    loadProcesses();
  } catch (e) {
    toast(e.message, false);
  }
}

async function scheduleRunNow(project) {
  try {
    const res = await api('POST', '/api/v1/projects/' + encodeURIComponent(project) + '/schedule-run');
    toast('schedule:run ' + project + ' selesai (exit ' + res.exit_code + ').');
    loadProcesses();
  } catch (e) {
    toast(e.message, false);
  }
}

async function showProcessLog(project, name) {
  try {
    const data = await api('GET', '/api/v1/projects/' + encodeURIComponent(project) + '/processes/' + encodeURIComponent(name) + '/log');
    $('log-title').textContent = project + '/' + name;
    $('log-body').textContent = data.log || '(log masih kosong)';
    $('log-panel').hidden = false;
    $('log-panel').scrollIntoView({ behavior: 'smooth', block: 'center' });
  } catch (e) {
    toast(e.message, false);
  }
}

// ---------- process definition editor (in the project form) ----------

function addProcessRow(def) {
  def = def || { name: '', command: '', kind: '', autostart: true, restart: 'on-failure' };
  const row = el('div', undefined, 'proc-row');
  const name = document.createElement('input');
  name.placeholder = 'nama (queue)';
  name.value = def.name || '';
  name.className = 'pr-name';
  const cmd = document.createElement('input');
  cmd.placeholder = 'perintah (artisan queue:work)';
  cmd.value = def.command || '';
  cmd.className = 'pr-command';
  const kind = el('select');
  const k1 = el('option', 'Proses panjang'); k1.value = '';
  const k2 = el('option', 'Scheduler (schedule:run)'); k2.value = 'schedule:run';
  kind.append(k1, k2);
  kind.value = def.kind || '';
  kind.className = 'pr-kind';
  const auto = document.createElement('input');
  auto.type = 'checkbox';
  auto.checked = !!def.autostart;
  auto.className = 'pr-autostart';
  const autoLabel = el('label', ' autostart');
  autoLabel.prepend(auto);
  const rm = button('Hapus', () => row.remove());
  row.append(name, cmd, kind, autoLabel, rm);
  $('pf-processes').appendChild(row);
}

function collectProcesses() {
  const out = [];
  for (const row of $('pf-processes').querySelectorAll('.proc-row')) {
    const name = row.querySelector('.pr-name').value.trim();
    if (!name) continue;
    const kind = row.querySelector('.pr-kind').value;
    out.push({
      name: name,
      command: kind === 'schedule:run' ? '' : row.querySelector('.pr-command').value.trim(),
      kind: kind,
      autostart: row.querySelector('.pr-autostart').checked,
      restart: 'on-failure',
    });
  }
  return out;
}

// ---------- activity log ----------

async function loadPanelLog() {
  const text = $('panel-log-text');
  const pathNote = $('panel-log-path');
  try {
    const data = await api('GET', '/api/v1/logs?lines=300');
    pathNote.textContent = 'File: ' + data.path + ' (' + data.total + ' baris)';
    text.textContent = data.lines.length ? data.lines.join('\n') : '(log masih kosong)';
  } catch (err) {
    text.textContent = 'Gagal memuat log: ' + err.message;
  }
}

// ---------- Fase 2c: Konfigurasi Apache ----------

let apacheConfigFiles = [];
let apacheModulesInitial = {};

async function loadApacheConfig() {
  try {
    const f = await api('GET', '/api/v1/apache/fcgid');
    const v = f.fcgid || {};
    $('fcgid-max-processes').value = v.max_processes || '';
    $('fcgid-max-processes-per-class').value = v.max_processes_per_class || '';
    $('fcgid-max-requests-per-process').value = v.max_requests_per_process || '';
    $('fcgid-max-request-len').value = v.max_request_len || '';
    $('fcgid-io-timeout').value = v.io_timeout || '';
    $('fcgid-busy-timeout').value = v.busy_timeout || '';
    $('fcgid-idle-timeout').value = v.idle_timeout || '';
    $('fcgid-note').textContent = 'Nilai yang tampil adalah nilai efektif (kosong di registry = default).';
  } catch (e) {
    toast('Gagal memuat pengaturan fcgid: ' + e.message, false);
  }
  try {
    const m = await api('GET', '/api/v1/apache/modules');
    const list = $('apache-modules-list');
    list.innerHTML = '';
    apacheModulesInitial = {};
    for (const mod of m.modules || []) {
      const row = el('div');
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.dataset.name = mod.name;
      cb.checked = mod.enabled;
      apacheModulesInitial[mod.name] = mod.enabled;
      // Pola yang sama dengan extension PHP bawaan: yang terkunci
      // tampil tercentang tapi tidak bisa dimatikan.
      if (mod.locked && mod.enabled) cb.disabled = true;
      row.appendChild(cb);
      row.appendChild(document.createTextNode(' ' + mod.name + ' — ' + mod.file + (mod.locked ? ' · terkunci: ' + (mod.locked_reason || '') : '')));
      list.appendChild(row);
    }
  } catch (e) {
    toast('Gagal memuat modul Apache: ' + e.message, false);
  }
  try {
    const c = await api('GET', '/api/v1/apache/config');
    apacheConfigFiles = c.files || [];
    const sel = $('apache-config-file');
    sel.innerHTML = '';
    for (const f of apacheConfigFiles) {
      const o = el('option', f.name + (f.generated ? ' (generated)' : ''));
      o.value = f.name;
      sel.appendChild(o);
    }
    await loadApacheConfigFile();
  } catch (e) {
    toast('Gagal memuat daftar config: ' + e.message, false);
  }
}

async function saveFcgid(ev) {
  ev.preventDefault();
  const num = (id) => parseInt($(id).value, 10) || 0;
  try {
    await api('PUT', '/api/v1/apache/fcgid', { fcgid: {
      max_processes: num('fcgid-max-processes'),
      max_processes_per_class: num('fcgid-max-processes-per-class'),
      max_requests_per_process: num('fcgid-max-requests-per-process'),
      max_request_len: num('fcgid-max-request-len'),
      io_timeout: num('fcgid-io-timeout'),
      busy_timeout: num('fcgid-busy-timeout'),
      idle_timeout: num('fcgid-idle-timeout'),
    }});
    toast('Pengaturan fcgid disimpan & di-apply (bertahan di apply berikutnya).');
    await loadApacheConfig();
  } catch (e) {
    toast(e.message, false);
  }
}

async function saveApacheModules() {
  const changes = [];
  for (const cb of document.querySelectorAll('#apache-modules-list input[type=checkbox]')) {
    if (cb.checked !== apacheModulesInitial[cb.dataset.name]) changes.push({ name: cb.dataset.name, enabled: cb.checked });
  }
  if (!changes.length) {
    toast('Tidak ada perubahan modul.');
    return;
  }
  try {
    for (const ch of changes) {
      await api('POST', '/api/v1/apache/modules', ch);
    }
    toast('Modul disimpan (tiap perubahan di-backup, dites httpd -t, lalu restart Apache).');
    await loadApacheConfig();
  } catch (e) {
    toast(e.message, false);
  }
}

async function loadApacheConfigFile() {
  const name = $('apache-config-file').value;
  if (!name) return;
  const meta = apacheConfigFiles.find((f) => f.name === name);
  $('apache-config-durability').textContent = meta
    ? (meta.durable ? 'File ini bertahan di apply berikutnya.' : 'File generated: akan ditimpa apply berikutnya — edit di sini sifatnya darurat, untuk permanen pakai Pengaturan Fcgid di atas.')
    : '';
  try {
    const data = await api('GET', '/api/v1/apache/config/' + encodeURIComponent(name));
    $('apache-config-text').value = data.content || '';
    $('apache-config-note').textContent = 'Path: ' + (meta ? meta.path : name);
  } catch (e) {
    toast('Gagal memuat config: ' + e.message, false);
  }
}

async function saveApacheConfigFile() {
  const name = $('apache-config-file').value;
  if (!name) return;
  if (!window.confirm('Simpan ' + name + '? File lama di-backup, dites httpd -t, dan Apache di-restart otomatis bila tes lolos.')) return;
  try {
    const res = await api('PUT', '/api/v1/apache/config/' + encodeURIComponent(name), { content: $('apache-config-text').value });
    toast('Config disimpan & Apache di-restart. Backup: ' + (res.backup || '-'));
  } catch (e) {
    toast(e.message, false);
  }
}
