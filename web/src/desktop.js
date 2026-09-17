// Native Wails shell integration. Browser/server builds do not expose these
// endpoints, so this module quietly steps aside there.
import { $, esc } from './state.js';
import { cycleTheme } from './theme.js';

async function desktopRequest(path, options) {
  const response = await fetch(path, options);
  if (!response.ok) {
    let message = `Request failed (${response.status})`;
    try { message = (await response.json()).error || message; } catch {}
    throw new Error(message);
  }
  return response.json();
}

function recentTime(timestamp) {
  if (!timestamp) return '';
  const date = new Date(timestamp * 1000);
  const now = Date.now();
  const days = Math.floor((now - date.getTime()) / 86400000);
  if (days <= 0) return 'Today';
  if (days === 1) return 'Yesterday';
  if (days < 7) return `${days} days ago`;
  return date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

function showStartError(message) {
  let node = $('#start-error');
  if (!node) {
    node = document.createElement('p');
    node.id = 'start-error';
    node.className = 'start-error';
    $('.start-actions')?.after(node);
  }
  node.textContent = message;
}

async function openDesktopProject(path = '') {
  const button = $('#start-open');
  if (button) button.disabled = true;
  try {
    const suffix = path ? `?path=${encodeURIComponent(path)}` : '';
    const result = await desktopRequest('/desktop/open' + suffix, { method: 'POST' });
    if (result.reload) location.reload();
  } catch (error) {
    showStartError(error.message || String(error));
  } finally {
    if (button) button.disabled = false;
  }
}

function renderDesktopRecents(recents) {
  const list = $('#recent-projects');
  const empty = $('#recent-empty');
  const count = $('#recent-count');
  if (!list) return;
  list.replaceChildren();
  count.textContent = recents.length ? String(recents.length) : '';
  empty.hidden = recents.length !== 0;

  for (const project of recents) {
    const row = document.createElement('div');
    row.className = 'recent-row' + (project.exists ? '' : ' recent-missing');

    const open = document.createElement('button');
    open.className = 'recent-open';
    open.disabled = !project.exists;
    open.title = project.exists ? project.path : 'This folder no longer exists';
    open.innerHTML = `<span class="recent-icon" aria-hidden="true">${project.exists ? '◇' : '!'}</span>` +
      `<span class="recent-copy"><strong>${esc(project.name)}</strong><small>${esc(project.path)}</small></span>` +
      `<time>${project.exists ? recentTime(project.lastOpened) : 'Missing'}</time>`;
    if (project.exists) open.addEventListener('click', () => openDesktopProject(project.path));
    row.append(open);

    if (!project.exists) {
      const remove = document.createElement('button');
      remove.className = 'recent-remove';
      remove.textContent = 'Remove';
      remove.title = 'Remove from recent projects';
      remove.addEventListener('click', async () => {
        try {
          const result = await desktopRequest('/desktop/remove?path=' + encodeURIComponent(project.path), { method: 'POST' });
          renderDesktopRecents(result.recents || []);
        } catch (error) { showStartError(error.message || String(error)); }
      });
      row.append(remove);
    }
    list.append(row);
  }
}

// Returns true only when this Wails window is the project-less start window.
export async function initDesktop() {
  let state;
  try {
    state = await desktopRequest('/desktop/state');
  } catch {
    return false;
  }
  // CSS drag regions only work after the runtime installs its mouse handlers.
  // Load it in project AND welcome windows, but never in browser/server mode.
  // Keep the URL dynamic so the web bundler leaves this embedded Wails asset
  // to the native asset server instead of trying to resolve it from disk.
  const runtimeURL = '/wails/runtime.js';
  await import(runtimeURL);
  document.body.classList.add('native-desktop');
  document.body.classList.toggle('native-single-window', state.singleWindow === true);
  if (state.mode !== 'welcome') return false;

  document.body.classList.add('desktop-welcome');
  $('#app').hidden = true;
  const start = $('#desktop-start');
  start.hidden = false;
  document.title = 'Rivo';
  renderDesktopRecents(state.recents || []);

  $('#start-open')?.addEventListener('click', () => openDesktopProject());
  $('#start-new-window')?.addEventListener('click', () => desktopRequest('/desktop/new-window', { method: 'POST' }).catch(error => showStartError(error.message)));
  $('#start-theme')?.addEventListener('click', cycleTheme);
  return true;
}
