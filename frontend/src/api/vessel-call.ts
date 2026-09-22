
import { request } from './client';
import type { BerthingGateResult, DomainRecord } from '../types/domain';

export async function listVesselCall(page = 1, pageSize = 20, search = '') {
  return request<DomainRecord[]>(`/vessels?page=${page}&pageSize=${pageSize}&search=${encodeURIComponent(search)}`);
}
export async function createVesselCall(input: Partial<DomainRecord>) {
  return request<DomainRecord>('/vessels', { method: 'POST', body: JSON.stringify(input) });
}
export async function transitionVesselCall(id: number, status: string, expectedVersion: number, reason: string) {
  return request<DomainRecord>(`/vessels/${id}/transition`, {
    method: 'POST', body: JSON.stringify({ status, expectedVersion, reason }),
  });
}
// 实时预检靠泊放行闸门（不落库）。
export async function evaluateBerthingGate(id: number) {
  return request<BerthingGateResult>(`/vessels/${id}/gate-check`);
}
// 最近一次已落库的闸门核对结果，刷新页面后可回读。
export async function latestBerthingGateChecks() {
  return request<BerthingGateResult[]>('/vessels/gate-checks/latest');
}
