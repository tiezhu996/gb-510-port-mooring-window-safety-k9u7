
import type { ApiEnvelope, UserSession } from '../types/domain';

const TOKEN_KEY = 'domain-control-session';

export function getToken(): string {
  try { return JSON.parse(localStorage.getItem(TOKEN_KEY) || '{}').token || ''; } catch { return ''; }
}
export function getStoredSession(): UserSession | null {
  try {
    const value = JSON.parse(localStorage.getItem(TOKEN_KEY) || 'null') as UserSession | null;
    return value?.token && value.username && value.role ? value : null;
  } catch {
    return null;
  }
}
export function saveSession(session: unknown): void { localStorage.setItem(TOKEN_KEY, JSON.stringify(session)); }
export function clearSession(): void { localStorage.removeItem(TOKEN_KEY); }

// ApiError 保留错误码与响应体数据，靠泊闸门用 data.blockers 回传阻断项。
export class ApiError extends Error {
  code: string;
  status: number;
  data?: unknown;
  constructor(status: number, code: string, message: string, data?: unknown) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.data = data;
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<ApiEnvelope<T>> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body) headers.set('Content-Type', 'application/json');
  const token = getToken();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const response = await fetch(`/api${path}`, { ...init, headers });
  if (response.status === 204) return { data: undefined as T };
  const payload = await response.json().catch(() => ({ error: 'invalid_response', message: '服务返回了无法解析的响应' }));
  if (!response.ok) throw new ApiError(response.status, payload.error || 'request_failed', payload.message || payload.error || `HTTP ${response.status}`, payload.data);
  return payload as ApiEnvelope<T>;
}
