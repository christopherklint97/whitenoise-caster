import { getAuthHeader, setAuthHeader, clearAuthHeader, authFetch } from './api';
import { initUI, showLogin, hideLogin, startSession } from './ui';
import { initTimer } from './timer';

// Wire up modules
initUI();
initTimer();

// Login form handler
const loginForm = document.getElementById('loginForm') as HTMLFormElement;
const loginError = document.getElementById('loginError')!;

loginForm.addEventListener('submit', async (e) => {
  e.preventDefault();
  const user = (document.getElementById('loginUser') as HTMLInputElement).value;
  const pass = (document.getElementById('loginPass') as HTMLInputElement).value;
  setAuthHeader('Basic ' + btoa(user + ':' + pass));
  try {
    const res = await authFetch('/api/status');
    if (res.status === 401) {
      clearAuthHeader();
      loginError.textContent = 'Wrong username or password';
      return;
    }
    localStorage.setItem('auth', getAuthHeader());
    hideLogin();
    startSession();
  } catch {
    loginError.textContent = 'Connection error';
  }
});

// Service worker
if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js');

// Start or show login
if (getAuthHeader()) {
  authFetch('/api/status').then(res => {
    if (res.status === 401) {
      clearAuthHeader();
      localStorage.removeItem('auth');
      showLogin();
    } else {
      startSession();
    }
  }).catch(() => showLogin());
} else {
  showLogin();
}
