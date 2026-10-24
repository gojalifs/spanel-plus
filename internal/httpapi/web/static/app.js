// SPanel+ dashboard (M3): projects CRUD, PHP version manager, config
// preview diff -> apply. All state changes go through /api/v1; the UI
// holds no logic beyond rendering and confirming.
'use strict';

const $ = (id) => document.getElementById(id);

async function api(method, path, body) {
  const opts = { method: method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(data.error || ('HTTP ' + res.status));
  }
  return data;
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
    registry = await api('GET', '/api/v1/projects');
    renderPHP();
    renderProjects();
  } catch (e) {
    toast('Gagal memuat data: ' + e.message, false);
  }
  loadDoctor();
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

async function showModules(version) {
  try {
    const data = await api('GET', '/api/v1/php/' + encodeURIComponent(version) + '/modules');
    $('php-modules-title').textContent = 'PHP ' + version;
    $('php-modules-list').textContent = data.modules.join(', ');
    $('php-modules').hidden = false;
  } catch (e) {
    toast('Gagal membaca extension: ' + e.message, false);
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
    a.href = 'http://' + window.location.hostname + p.url_path;
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
  $('project-form').scrollIntoView({ behavior: 'smooth', block: 'center' });
}

function resetForm() {
  editingName = null;
  $('project-form-title').textContent = 'Tambah proyek';
  $('pf-submit').textContent = 'Tambah & apply';
  $('pf-name').disabled = false;
  $('project-form').reset();
  $('pf-enabled').checked = true;
}

document.addEventListener('DOMContentLoaded', () => {
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

  loadAll();
});
