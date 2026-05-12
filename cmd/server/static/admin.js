(function () {
  const $rows = document.getElementById('rows');
  const $count = document.getElementById('count');
  const $error = document.getElementById('error');
  const $refresh = document.getElementById('refresh');

  function showError(msg) {
    $error.textContent = msg;
    $error.hidden = false;
  }

  function clearError() {
    $error.hidden = true;
    $error.textContent = '';
  }

  function fmtDate(iso) {
    if (!iso) return '—';
    try {
      const d = new Date(iso);
      return d.toLocaleString('it-IT', { dateStyle: 'short', timeStyle: 'medium' });
    } catch {
      return iso;
    }
  }

  function fmtSize(n) {
    if (n < 1024) return `${n} B`;
    if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
    return `${(n / 1024 / 1024).toFixed(2)} MB`;
  }

  function makeCell(text, cls, label) {
    const td = document.createElement('td');
    if (cls) td.className = cls;
    if (label) td.setAttribute('data-label', label);
    td.textContent = text;
    return td;
  }

  function renderRows(sessions) {
    $rows.innerHTML = '';
    if (sessions.length === 0) {
      const tr = document.createElement('tr');
      const td = document.createElement('td');
      td.colSpan = 8;
      td.className = 'muted center';
      td.textContent = 'Nessuna sessione attiva.';
      tr.appendChild(td);
      $rows.appendChild(tr);
      return;
    }
    sessions.forEach((s) => {
      const tr = document.createElement('tr');

      const slugCell = document.createElement('td');
      slugCell.setAttribute('data-label', 'Slug');
      const link = document.createElement('a');
      link.href = `/s/${encodeURIComponent(s.slug)}`;
      link.textContent = s.slug;
      link.target = '_blank';
      link.rel = 'noopener';
      link.className = 'slug-link';
      slugCell.appendChild(link);
      tr.appendChild(slugCell);

      tr.appendChild(makeCell(s.name || '—', s.name ? '' : 'muted', 'Nome'));

      const typeCell = document.createElement('td');
      typeCell.setAttribute('data-label', 'Tipo');
      const tag = document.createElement('span');
      tag.className = `tag ${s.type}`;
      tag.textContent = s.type === 'persistent' ? 'Persistente' : 'Temporanea';
      typeCell.appendChild(tag);
      tr.appendChild(typeCell);

      const totalSize = (s.total_size != null) ? s.total_size : (s.content_size + (s.files_size || 0));
      const sizeLabel = s.files_count > 0
        ? `${fmtSize(totalSize)} (${fmtSize(s.content_size)} testo + ${fmtSize(s.files_size || 0)} in ${s.files_count} file)`
        : fmtSize(totalSize);
      tr.appendChild(makeCell(sizeLabel, '', 'Size'));
      tr.appendChild(makeCell(fmtDate(s.created_at), '', 'Creata'));
      tr.appendChild(makeCell(fmtDate(s.updated_at), '', 'Aggiornata'));
      tr.appendChild(makeCell(s.expires_at ? fmtDate(s.expires_at) : '—', s.expires_at ? '' : 'muted', 'Scade'));

      const actionsCell = document.createElement('td');
      actionsCell.className = 'col-actions';
      const del = document.createElement('button');
      del.className = 'danger';
      del.textContent = 'Elimina';
      del.addEventListener('click', () => confirmDelete(s));
      actionsCell.appendChild(del);
      tr.appendChild(actionsCell);

      $rows.appendChild(tr);
    });
  }

  async function load() {
    clearError();
    $count.textContent = 'Caricamento...';
    try {
      const res = await fetch('/admin/api/sessions', { credentials: 'same-origin' });
      if (res.status === 401) {
        showError('Non autorizzato. Ricarica e inserisci le credenziali.');
        $count.textContent = '';
        return;
      }
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const data = await res.json();
      $count.textContent = `${data.count} sessione/i.`;
      renderRows(data.sessions || []);
    } catch (e) {
      showError(`Errore caricamento: ${e.message}`);
      $count.textContent = '';
    }
  }

  async function confirmDelete(s) {
    const label = s.name ? `${s.name} (${s.slug})` : s.slug;
    if (!confirm(`Eliminare definitivamente la sessione "${label}"?\nL'operazione è irreversibile.`)) return;
    clearError();
    try {
      const res = await fetch(`/admin/api/sessions/${encodeURIComponent(s.slug)}`, {
        method: 'DELETE',
        credentials: 'same-origin',
      });
      if (res.status === 404) {
        showError('Sessione non trovata (forse già eliminata).');
      } else if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      await load();
    } catch (e) {
      showError(`Errore eliminazione: ${e.message}`);
    }
  }

  $refresh.addEventListener('click', load);
  load();
})();
