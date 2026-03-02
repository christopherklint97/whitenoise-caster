import type { StatusResponse, Speaker, TimerRequest } from './types';

let authHeader = localStorage.getItem('auth') || '';

export function getAuthHeader(): string {
  return authHeader;
}

export function setAuthHeader(value: string): void {
  authHeader = value;
}

export function clearAuthHeader(): void {
  authHeader = '';
}

export function authFetch(url: string, opts: RequestInit = {}): Promise<Response> {
  if (authHeader) {
    opts.headers = { ...opts.headers as Record<string, string>, Authorization: authHeader };
  }
  return fetch(url, opts);
}

export async function play(speakerIP: string): Promise<Response> {
  return authFetch('/api/play', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ speaker_ip: speakerIP }),
  });
}

export async function pause(): Promise<Response> {
  return authFetch('/api/pause', { method: 'POST' });
}

export async function stop(): Promise<Response> {
  return authFetch('/api/stop', { method: 'POST' });
}

export async function setVolume(level: number): Promise<Response> {
  return authFetch('/api/volume', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ level }),
  });
}

export async function getStatus(): Promise<StatusResponse> {
  const res = await authFetch('/api/status');
  if (res.status === 401) throw new AuthError();
  return res.json();
}

export async function getSpeakers(): Promise<Speaker[]> {
  const res = await authFetch('/api/speakers');
  if (res.status === 401) throw new AuthError();
  return res.json();
}

export async function setTimer(payload: TimerRequest): Promise<Response> {
  return authFetch('/api/timer', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function cancelTimer(): Promise<Response> {
  return authFetch('/api/timer', { method: 'DELETE' });
}

export class AuthError extends Error {
  constructor() {
    super('Unauthorized');
    this.name = 'AuthError';
  }
}
