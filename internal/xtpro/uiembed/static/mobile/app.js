const API_BASE = `${window.location.origin}/api/v1`;
function xtproBasePrefix() {
  const path = window.location.pathname || "/";
  const idx = path.indexOf("/dashboard");
  if (idx > 0) return path.slice(0, idx);
  const parts = path.split("/").filter(Boolean);
  return parts.length ? `/${parts[0]}` : "";
}

const BASE_PREFIX = xtproBasePrefix();
const WS_ENDPOINT = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}${BASE_PREFIX}/api/v1/dashboard/ws`;
const THEME_KEY = 'xtpro-mobile-theme';
const FETCH_TIMEOUT_MS = 3500;

let ws;
let reconnectTimer;
/** @type {any[] | null} */
let lastTunnelsCache = null;
/** null = chưa có trạng thái WS (đang kết nối) */
let mobileConnOnline = null;

const elements = {
    themeToggle: document.getElementById('themeToggle'),
    langToggle: document.getElementById('langToggle'),
    connectionStatus: document.getElementById('connectionStatus'),
    connectionValue: document.querySelector('.connection-value'),
    manualRefresh: document.getElementById('manualRefresh'),
    statActive: document.getElementById('statActiveTunnels'),
    statTotalConnections: document.getElementById('statTotalConnections'),
    statUpload: document.getElementById('statTotalUpload'),
    statDownload: document.getElementById('statTotalDownload'),
    lastUpdated: document.getElementById('lastUpdated'),
    tunnelsList: document.getElementById('tunnelsList'),
    emptyState: document.getElementById('emptyState'),
    toastContainer: document.getElementById('toastContainer'),
    actionDialog: document.getElementById('actionDialog'),
    dialogTitle: document.getElementById('dialogTitle'),
    dialogDescription: document.getElementById('dialogDescription'),
    dialogCode: document.getElementById('dialogCode'),
    dialogClose: document.getElementById('dialogClose'),
    copyAction: document.getElementById('copyAction'),
};

document.addEventListener('DOMContentLoaded', () => {
    applyInitialTheme();
    wireEvents();
    establishWebSocket();
    loadInitialData();
});

function wireEvents() {
    elements.themeToggle?.addEventListener('click', toggleTheme);
    elements.langToggle?.addEventListener('click', toggleLanguage);
    elements.manualRefresh?.addEventListener('click', () => {
        loadInitialData(true);
    });

    document.querySelectorAll('.action-card').forEach((card) => {
        card.addEventListener('click', () => presentAction(card));
    });

    elements.dialogClose?.addEventListener('click', () => closeDialog());
    elements.copyAction?.addEventListener('click', handleCopyAction);

    elements.actionDialog?.addEventListener('close', () => {
        elements.dialogCode.textContent = '';
    });
}

function applyInitialTheme() {
    const saved = localStorage.getItem(THEME_KEY) || 'dark';
    applyTheme(saved);
}

function toggleTheme() {
    const current = document.body.getAttribute('data-theme') || 'dark';
    applyTheme(current === 'dark' ? 'light' : 'dark');
}

function applyTheme(theme) {
    document.body.setAttribute('data-theme', theme);
    localStorage.setItem(THEME_KEY, theme);

    if (elements.themeToggle) {
        const icon = elements.themeToggle.querySelector('.ghost-btn-icon');
        if (icon) icon.textContent = theme === 'dark' ? '🌙' : '☀️';
    }
}

function getI18n() {
    return window.XTProI18n;
}

async function toggleLanguage() {
    const i18n = getI18n();
    if (!i18n) return;
    const next = i18n.getLang() === 'vi' ? 'en' : 'vi';
    i18n.setLang(next);
    await i18n.loadDict(next);
    i18n.applyTranslations(document);
    updateLangToggle();
    refreshMobileAfterI18n();
}

function updateLangToggle() {
    const i18n = getI18n();
    if (!elements.langToggle || !i18n) return;
    elements.langToggle.textContent = i18n.getLang() === 'vi' ? 'EN' : 'VI';
}

window.addEventListener('xtpro:i18n:ready', () => {
    updateLangToggle();
    if (mobileConnOnline === null && elements.connectionValue) {
        const i18n = getI18n();
        if (i18n) elements.connectionValue.textContent = i18n.t('common.status.connecting');
    }
});
window.addEventListener('xtpro:i18n:applied', () => {
    updateLangToggle();
    refreshMobileAfterI18n();
});

function refreshMobileAfterI18n() {
    const i18n = getI18n();
    if (!i18n) return;
    if (mobileConnOnline === null && elements.connectionValue) {
        elements.connectionValue.textContent = i18n.t('common.status.connecting');
    } else {
        setConnectionState(!!mobileConnOnline);
    }
    updateLastUpdated();
    if (lastTunnelsCache && lastTunnelsCache.length > 0) {
        renderTunnels(lastTunnelsCache);
    }
}

async function loadInitialData(showToastMessage = false) {
    try {
        const results = await Promise.allSettled([
            fetchJSON(`${API_BASE}/metrics`),
            fetchJSON(`${API_BASE}/tunnels`),
        ]);

        const metrics = results[0].status === 'fulfilled' ? results[0].value : undefined;
        const tunnels = results[1].status === 'fulfilled' ? results[1].value : undefined;

        if (metrics?.success) updateMetrics(metrics.data);
        if (tunnels?.success) renderTunnels(tunnels.data);

        updateLastUpdated();
        const i18n = getI18n();
        notify(i18n ? i18n.t('mobile.toastRefresh') : 'Đã làm mới dữ liệu', 'success', showToastMessage);
    } catch (error) {
        console.error('Initial load failed', error);
        const i18n = getI18n();
        notify(i18n ? i18n.t('users.connectionError') : 'Connection error', 'error', showToastMessage);
    }
}

async function fetchJSON(url) {
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
    const response = await fetch(url, { signal: controller.signal }).finally(() => clearTimeout(timeoutId));
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    return response.json();
}

function establishWebSocket() {
    try {
        ws = new WebSocket(WS_ENDPOINT);
        ws.addEventListener('open', () => {
            setConnectionState(true);
            const i18n = getI18n();
            notify(i18n ? i18n.t('mobile.toastWsConnected') : 'Realtime connected', 'success');
            if (reconnectTimer) {
                clearTimeout(reconnectTimer);
                reconnectTimer = undefined;
            }
        });

        ws.addEventListener('message', ({ data }) => {
            try {
                const payload = JSON.parse(data);
                handleRealtime(payload);
            } catch (err) {
                console.warn('Invalid WS payload', err);
            }
        });

        ws.addEventListener('close', () => {
            setConnectionState(false);
            if (!reconnectTimer) {
                reconnectTimer = setTimeout(establishWebSocket, 4000);
            }
        });

        ws.addEventListener('error', (err) => {
            console.error('WebSocket error', err);
            setConnectionState(false);
            ws.close();
        });
    } catch (error) {
        console.error('WS init error', error);
        setConnectionState(false);
    }
}

function handleRealtime(payload) {
    if (payload.type === 'metrics') {
        updateMetrics(payload.data);
        updateLastUpdated();
    }

    if (payload.type === 'tunnel_update') {
        renderTunnels(payload.data);
    }
}

function updateMetrics(data = {}) {
    const active = data.activeTunnels ?? data.active_tunnels ?? 0;
    const totalConnections = data.totalConnections ?? data.total_connections ?? 0;
    const totalUp = humanBytes(data.totalBytesUp ?? data.total_bytes_up ?? 0);
    const totalDown = humanBytes(data.totalBytesDown ?? data.total_bytes_down ?? 0);

    if (elements.statActive) elements.statActive.textContent = active;
    if (elements.statTotalConnections) elements.statTotalConnections.textContent = totalConnections;
    if (elements.statUpload) elements.statUpload.textContent = totalUp;
    if (elements.statDownload) elements.statDownload.textContent = totalDown;
}

function renderTunnels(tunnels = []) {
    lastTunnelsCache = Array.isArray(tunnels) ? tunnels : [];
    if (!Array.isArray(tunnels) || tunnels.length === 0) {
        elements.tunnelsList.innerHTML = '';
        elements.emptyState.hidden = false;
        return;
    }

    elements.emptyState.hidden = true;
    elements.tunnelsList.innerHTML = tunnels.map(buildTunnelCard).join('');
}

function buildTunnelCard(tunnel) {
    const t = getI18n();
    const name = escapeHTML(tunnel.name || tunnel.label || 'Tunnel');
    const protocol = (tunnel.protocol || 'tcp').toLowerCase();
    const status = (tunnel.status || 'active').toLowerCase();
    const publicHost = tunnel.public_host || tunnel.publicHost || `${tunnel.remote_host || '0.0.0.0'}:${tunnel.public_port || '—'}`;
    const local = `${tunnel.local_host || tunnel.localHost || 'localhost'}:${tunnel.local_port || tunnel.localPort || '—'}`;
    const trafficUp = humanBytes(tunnel.bytes_up || tunnel.bytesUp || 0);
    const trafficDown = humanBytes(tunnel.bytes_down || tunnel.bytesDown || 0);
    const heartbeat = tunnel.last_heartbeat || tunnel.lastHeartbeat;
    const lbLocal = t ? t.t('mobile.tunnelLocal') : 'Local';
    const lbPublic = t ? t.t('mobile.tunnelPublic') : 'Public';
    const lbTraffic = t ? t.t('mobile.tunnelTraffic') : 'Traffic';
    const lbStatus = t ? t.t('mobile.tunnelStatusLabel') : 'Status';
    const lbHb = t ? t.t('mobile.heartbeat') : 'Heartbeat';
    const online = t ? t.t('mobile.statusOnline') : 'Online';
    const offline = t ? t.t('mobile.statusOffline') : 'Offline';

    return `
        <article class="tunnel-card">
            <header>
                <div class="tunnel-name">${name}</div>
                <span class="badge ${protocol}">${protocol.toUpperCase()}</span>
            </header>
            <div class="tunnel-meta">
                <div>
                    <strong>${lbLocal}</strong>
                    <span>${escapeHTML(local)}</span>
                </div>
                <div>
                    <strong>${lbPublic}</strong>
                    <span>${escapeHTML(publicHost)}</span>
                </div>
                <div>
                    <strong>${lbTraffic}</strong>
                    <span>↑ ${trafficUp} • ↓ ${trafficDown}</span>
                </div>
                <div>
                    <strong>${lbStatus}</strong>
                    <span>${status === 'active' ? online : offline}</span>
                </div>
                ${heartbeat ? `<div><strong>${lbHb}</strong><span>${formatTime(heartbeat)}</span></div>` : ''}
            </div>
        </article>
    `;
}

function setConnectionState(isOnline) {
    mobileConnOnline = isOnline;
    elements.connectionStatus.classList.toggle('is-online', isOnline);
    elements.connectionStatus.classList.toggle('is-offline', !isOnline);

    if (elements.connectionValue) {
        const i18n = getI18n();
        elements.connectionValue.textContent = i18n
            ? (isOnline ? i18n.t('mobile.connOk') : i18n.t('mobile.connBad'))
            : (isOnline ? 'Connected' : 'Disconnected');
    }
}

function updateLastUpdated() {
    if (!elements.lastUpdated) return;
    const now = new Date();
    const i18n = getI18n();
    const loc = i18n?.getLang?.() === 'en' ? 'en-US' : 'vi-VN';
    const prefix = i18n ? i18n.t('mobile.updatedAt') : 'Updated';
    elements.lastUpdated.textContent = `${prefix} ${now.toLocaleTimeString(loc, { hour12: false })}`;
}

function notify(message, type = 'info', forced = false) {
    if (!elements.toastContainer || (!forced && type === 'success')) return;

    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.textContent = message;
    elements.toastContainer.appendChild(toast);

    setTimeout(() => {
        toast.classList.add('exit');
        toast.addEventListener('animationend', () => toast.remove(), { once: true });
    }, 2600);
}

function presentAction(card) {
    if (!elements.actionDialog) return;

    const subtitle = card.querySelector('.action-subtitle')?.textContent || '';
    const title = card.querySelector('.action-title')?.textContent || '';
    const i18n = getI18n();

    elements.dialogTitle.textContent = title;
    elements.dialogDescription.textContent = i18n ? i18n.t('mobile.dialogHint') : 'Copy and run on your client.';
    elements.dialogCode.textContent = subtitle;

    elements.actionDialog.showModal();
    elements.dialogCode.focus();
}

function closeDialog() {
    elements.actionDialog?.close();
}

async function handleCopyAction() {
    if (!elements.dialogCode?.textContent) return;

    try {
        await navigator.clipboard.writeText(elements.dialogCode.textContent.trim());
        const i18n = getI18n();
        notify(i18n ? i18n.t('mobile.toastCopyOk') : 'Copied', 'success', true);
    } catch (error) {
        console.error('Copy failed', error);
        const i18n = getI18n();
        notify(i18n ? i18n.t('mobile.toastCopyFail') : 'Copy failed', 'error', true);
    }
}

function humanBytes(bytes = 0) {
    if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
    const value = bytes / Math.pow(1024, exponent);
    return `${value.toFixed(value >= 10 ? 0 : 1)} ${units[exponent]}`;
}

function escapeHTML(str = '') {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function formatTime(timestamp) {
    const date = new Date(timestamp);
    if (Number.isNaN(date.getTime())) return '—';
    const loc = getI18n()?.getLang?.() === 'en' ? 'en-US' : 'vi-VN';
    return date.toLocaleString(loc, { hour12: false });
}

window.addEventListener('beforeunload', () => {
    if (ws) ws.close();
});
