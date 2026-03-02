import type { State, StatusResponse } from './types';
import { authFetch, play as apiPlay, setVolume, AuthError, getSpeakers } from './api';
import { updateTimerUI } from './timer';

let currentState: State = 'disconnected';
let activeSpeakerIP = '';
let pollTimer: ReturnType<typeof setInterval> | null = null;
let selectedVolume: number | null = null;

// DOM refs (assigned in initUI)
let btn: HTMLButtonElement;
let speakerSel: HTMLSelectElement;
let statusEl: HTMLElement;
let playIcon: HTMLElement;
let pauseIcon: HTMLElement;
let stopIcon: HTMLElement;
let loginOverlay: HTMLElement;
let loginForm: HTMLFormElement;
let loginError: HTMLElement;

// Long-press state
let pressTimer: ReturnType<typeof setTimeout>;
let longPressFired = false;

function showIcon(state: State): void {
  playIcon.style.display = 'none';
  pauseIcon.style.display = 'none';
  stopIcon.style.display = 'none';
  if (state === 'playing') pauseIcon.style.display = '';
  else playIcon.style.display = '';
}

function updateUI(data: StatusResponse): void {
  currentState = data.state;
  activeSpeakerIP = data.speaker_ip || '';
  btn.className = 'play-btn' + (data.state !== 'disconnected' ? ' ' + data.state : '');
  showIcon(data.state);

  statusEl.className = 'status' + (data.state === 'error' ? ' error-text' : '');
  switch (data.state) {
    case 'disconnected': statusEl.textContent = 'Not connected'; break;
    case 'connecting': statusEl.textContent = 'Connecting to ' + (data.speaker_name || '...'); break;
    case 'playing': statusEl.textContent = 'Playing on ' + (data.speaker_name || 'speaker'); break;
    case 'paused': statusEl.textContent = 'Paused on ' + (data.speaker_name || 'speaker'); break;
    case 'error': statusEl.textContent = data.error || 'Error'; break;
    default: statusEl.textContent = data.state;
  }

  updateTimerUI(data);
}

export function showLogin(): void {
  loginOverlay.classList.remove('hidden');
  loginError.textContent = '';
}

export function hideLogin(): void {
  loginOverlay.classList.add('hidden');
}

export async function loadSpeakers(): Promise<void> {
  try {
    const speakers = await getSpeakers();
    speakerSel.innerHTML = '';
    const saved = localStorage.getItem('lastSpeaker');
    speakers.forEach(s => {
      const opt = document.createElement('option');
      opt.value = s.ip;
      opt.textContent = s.name;
      if (s.ip === saved) opt.selected = true;
      speakerSel.appendChild(opt);
    });
  } catch (e) {
    if (e instanceof AuthError) { showLogin(); return; }
    speakerSel.innerHTML = '<option value="">Failed to load</option>';
  }
}

export async function pollStatus(): Promise<void> {
  try {
    const res = await authFetch('/api/status');
    if (res.status === 401) { showLogin(); return; }
    const data: StatusResponse = await res.json();
    updateUI(data);
  } catch { /* ignore */ }
}

export function startSession(): void {
  loadSpeakers();
  pollStatus();
  if (pollTimer) clearInterval(pollTimer);
  pollTimer = setInterval(pollStatus, 3000);
}

export function getCurrentState(): State {
  return currentState;
}

export function initUI(): void {
  btn = document.getElementById('playBtn') as HTMLButtonElement;
  speakerSel = document.getElementById('speaker') as HTMLSelectElement;
  statusEl = document.getElementById('status')!;
  playIcon = document.getElementById('playIcon')!;
  pauseIcon = document.getElementById('pauseIcon')!;
  stopIcon = document.getElementById('stopIcon')!;
  loginOverlay = document.getElementById('loginOverlay')!;
  loginForm = document.getElementById('loginForm') as HTMLFormElement;
  loginError = document.getElementById('loginError')!;

  // Play/pause click
  btn.addEventListener('click', async () => {
    if (longPressFired) return;
    if (currentState === 'connecting') return;

    const selectedIP = speakerSel.value;

    // If playing/paused on the same speaker, toggle pause
    if ((currentState === 'playing' || currentState === 'paused') && selectedIP === activeSpeakerIP) {
      try {
        await authFetch('/api/pause', { method: 'POST' });
        pollStatus();
      } catch { /* ignore */ }
      return;
    }

    // Start playback (new speaker selected, or not currently playing)
    if (!selectedIP) return;
    localStorage.setItem('lastSpeaker', selectedIP);

    try {
      statusEl.textContent = 'Connecting...';
      btn.className = 'play-btn connecting';
      const res = await apiPlay(selectedIP);
      if (!res.ok) {
        const err = await res.json();
        updateUI({ state: 'error', error: err.error || 'Failed', timer: { active: false, remaining_s: 0, action: 'stop', volume_level: 0 } });
      } else {
        if (selectedVolume !== null) {
          try { await setVolume(selectedVolume); } catch { /* ignore */ }
        }
        pollStatus();
      }
    } catch {
      updateUI({ state: 'error', error: 'Network error', timer: { active: false, remaining_s: 0, action: 'stop', volume_level: 0 } });
    }
  });

  // Long press to stop
  btn.addEventListener('pointerdown', () => {
    longPressFired = false;
    if (currentState === 'disconnected') return;
    pressTimer = setTimeout(async () => {
      longPressFired = true;
      try {
        await authFetch('/api/stop', { method: 'POST' });
        pollStatus();
      } catch { /* ignore */ }
    }, 800);
  });
  btn.addEventListener('pointerup', () => clearTimeout(pressTimer));
  btn.addEventListener('pointerleave', () => clearTimeout(pressTimer));

  // Volume buttons — always selectable; applied immediately if connected,
  // otherwise stored and applied when playback starts.
  document.querySelectorAll('.vol-btn').forEach(b => {
    b.addEventListener('click', async () => {
      const level = parseFloat((b as HTMLElement).dataset.level!);
      document.querySelectorAll('.vol-btn').forEach(v => v.classList.remove('active'));
      b.classList.add('active');
      selectedVolume = level;

      if (currentState === 'playing' || currentState === 'paused') {
        try { await setVolume(level); } catch { /* ignore */ }
      }
    });
  });
}
