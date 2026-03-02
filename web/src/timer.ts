import type { StatusResponse, TimerRequest } from './types';
import { setTimer, cancelTimer } from './api';
import { pollStatus } from './ui';

let timerActionMode: 'stop' | 'volume' = 'stop';

// DOM refs (assigned in initTimer)
let timerToggleBtn: HTMLButtonElement;
let timerSetup: HTMLElement;
let timerActive: HTMLElement;
let timerCountdown: HTMLElement;
let timerActionLabel: HTMLElement;
let timerCancelBtn: HTMLButtonElement;
let timerSetBtn: HTMLButtonElement;
let timerDismissBtn: HTMLButtonElement;
let timerActStop: HTMLButtonElement;
let timerActVol: HTMLButtonElement;
let timerVolRow: HTMLElement;
let wheelHours: HTMLElement;
let wheelMinutes: HTMLElement;

export function formatCountdown(seconds: number): string {
  if (seconds <= 0) return '00:00';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  const pad = (n: number) => (n < 10 ? '0' + n : '' + n);
  if (h > 0) return h + ':' + pad(m) + ':' + pad(s);
  return pad(m) + ':' + pad(s);
}

export function buildWheel(container: HTMLElement, count: number, padZero: boolean): void {
  container.innerHTML = '';
  for (let i = 0; i < 2; i++) {
    const sp = document.createElement('div');
    sp.className = 'wheel-item spacer';
    container.appendChild(sp);
  }
  for (let i = 0; i < count; i++) {
    const item = document.createElement('div');
    item.className = 'wheel-item';
    item.textContent = padZero && i < 10 ? '0' + i : '' + i;
    item.dataset.value = '' + i;
    container.appendChild(item);
  }
  for (let i = 0; i < 2; i++) {
    const sp = document.createElement('div');
    sp.className = 'wheel-item spacer';
    container.appendChild(sp);
  }
}

export function getWheelValue(container: HTMLElement): number {
  const scrollTop = container.scrollTop;
  const idx = Math.round(scrollTop / 44);
  return Math.max(0, idx);
}

function updateWheelSelected(container: HTMLElement): void {
  const items = container.querySelectorAll('.wheel-item:not(.spacer)');
  const val = getWheelValue(container);
  items.forEach((item, i) => {
    item.classList.toggle('selected', i === val);
  });
}

export function setWheelValue(container: HTMLElement, val: number): void {
  container.scrollTop = val * 44;
  updateWheelSelected(container);
}

export function updateTimerUI(data: StatusResponse): void {
  const connected = data.state === 'playing' || data.state === 'paused';
  timerToggleBtn.disabled = !connected;

  if (data.timer && data.timer.active) {
    timerToggleBtn.style.display = 'none';
    timerSetup.classList.remove('visible');
    timerActive.classList.add('visible');
    timerCountdown.textContent = formatCountdown(data.timer.remaining_s);
    if (data.timer.action === 'volume') {
      timerActionLabel.textContent = 'Volume \u2192 ' + Math.round(data.timer.volume_level * 100) + '%';
    } else {
      timerActionLabel.textContent = 'Stop playback';
    }
  } else {
    timerActive.classList.remove('visible');
    timerToggleBtn.style.display = '';
    if (!connected) {
      timerSetup.classList.remove('visible');
    }
  }
}

export function initTimer(): void {
  timerToggleBtn = document.getElementById('timerToggleBtn') as HTMLButtonElement;
  timerSetup = document.getElementById('timerSetup')!;
  timerActive = document.getElementById('timerActive')!;
  timerCountdown = document.getElementById('timerCountdown')!;
  timerActionLabel = document.getElementById('timerActionLabel')!;
  timerCancelBtn = document.getElementById('timerCancelBtn') as HTMLButtonElement;
  timerSetBtn = document.getElementById('timerSetBtn') as HTMLButtonElement;
  timerDismissBtn = document.getElementById('timerDismissBtn') as HTMLButtonElement;
  timerActStop = document.getElementById('timerActStop') as HTMLButtonElement;
  timerActVol = document.getElementById('timerActVol') as HTMLButtonElement;
  timerVolRow = document.getElementById('timerVolRow')!;
  wheelHours = document.getElementById('wheelHours')!;
  wheelMinutes = document.getElementById('wheelMinutes')!;

  buildWheel(wheelHours, 13, false);
  buildWheel(wheelMinutes, 60, true);

  wheelHours.addEventListener('scroll', () => updateWheelSelected(wheelHours));
  wheelMinutes.addEventListener('scroll', () => updateWheelSelected(wheelMinutes));

  // Default: 0h 30m
  setTimeout(() => {
    setWheelValue(wheelHours, 0);
    setWheelValue(wheelMinutes, 30);
  }, 50);

  timerToggleBtn.addEventListener('click', () => {
    timerSetup.classList.toggle('visible');
  });

  timerDismissBtn.addEventListener('click', () => {
    timerSetup.classList.remove('visible');
  });

  timerActStop.addEventListener('click', () => {
    timerActionMode = 'stop';
    timerActStop.classList.add('active');
    timerActVol.classList.remove('active');
    timerVolRow.classList.remove('visible');
  });

  timerActVol.addEventListener('click', () => {
    timerActionMode = 'volume';
    timerActVol.classList.add('active');
    timerActStop.classList.remove('active');
    timerVolRow.classList.add('visible');
  });

  timerSetBtn.addEventListener('click', async () => {
    const hours = getWheelValue(wheelHours);
    const minutes = getWheelValue(wheelMinutes);
    const totalS = hours * 3600 + minutes * 60;
    if (totalS < 1) return;

    const body: TimerRequest = { duration_s: totalS, action: timerActionMode };
    if (timerActionMode === 'volume') {
      body.volume_level = 0.2;
    }

    try {
      const res = await setTimer(body);
      if (res.ok) {
        timerSetup.classList.remove('visible');
        pollStatus();
      }
    } catch { /* ignore */ }
  });

  timerCancelBtn.addEventListener('click', async () => {
    try {
      await cancelTimer();
      pollStatus();
    } catch { /* ignore */ }
  });
}
