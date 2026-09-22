import { defineStore } from 'pinia';
import { request, ApiError } from '../api/client';
import { evaluateBerthingGate, latestBerthingGateChecks } from '../api/vessel-call';
import type { BerthingGateResult, DomainRecord, GateBlocker, PageMeta } from '../types/domain';

interface GateFailure {
  message: string;
  blockers: GateBlocker[];
  result?: BerthingGateResult;
}

export const useVesselCallStore = defineStore('vesselCall', {
  state: () => ({
    items: [] as DomainRecord[],
    meta: { page: 1, pageSize: 20, total: 0 } as PageMeta,
    loading: false,
    error: '',
    gateResults: [] as BerthingGateResult[],
    gateLoading: false,
    gateError: '',
    // 推进失败（闸门阻断）时的结构化原因，供靠泊页直接展示。
    gateFailure: null as GateFailure | null,
  }),
  getters: {
    gateByVessel: (state) => {
      const map = new Map<number, BerthingGateResult>();
      state.gateResults.forEach((result) => map.set(result.vesselId, result));
      return map;
    },
  },
  actions: {
    async load(path: string, search = '') {
      this.loading = true;
      this.error = '';
      try {
        const result = await request<DomainRecord[]>(`/${path}?page=1&pageSize=20&search=${encodeURIComponent(search)}`);
        this.items = result.data;
        this.meta = result.meta || { page: 1, pageSize: 20, total: result.data.length };
        await this.loadGateResults();
      } catch (error) {
        this.error = error instanceof Error ? error.message : String(error);
      } finally {
        this.loading = false;
      }
    },
    async createRecord(path: string, input: Partial<DomainRecord>) {
      this.loading = true;
      try {
        await request<DomainRecord>(`/${path}`, { method: 'POST', body: JSON.stringify(input) });
        await this.load(path);
      } finally {
        this.loading = false;
      }
    },
    async loadGateResults() {
      this.gateLoading = true;
      try {
        const result = await latestBerthingGateChecks();
        this.gateResults = result.data;
      } catch (error) {
        this.gateError = error instanceof Error ? error.message : String(error);
      } finally {
        this.gateLoading = false;
      }
    },
    async evaluateGate(item: DomainRecord) {
      this.gateLoading = true;
      this.gateError = '';
      try {
        const result = await evaluateBerthingGate(item.id);
        const next = result.data;
        // 实时预检结果只在本地替换展示，不写入 gateResults（未持久化）。
        const others = this.gateResults.filter((entry) => entry.vesselId !== item.id);
        this.gateResults = [next, ...others];
        return next;
      } catch (error) {
        this.gateError = error instanceof Error ? error.message : String(error);
        throw error;
      } finally {
        this.gateLoading = false;
      }
    },
    async transition(path: string, item: DomainRecord, status: string) {
      this.loading = true;
      this.error = '';
      this.gateFailure = null;
      try {
        await request<DomainRecord>(`/${path}/${item.id}/transition`, {
          method: 'POST',
          body: JSON.stringify({ status, expectedVersion: item.version, reason: '前端工作台人工确认' }),
        });
      } catch (error) {
        if (error instanceof ApiError && error.code === 'berthing_gate_blocked') {
          const result = error.data as BerthingGateResult | undefined;
          this.gateFailure = {
            message: error.message,
            blockers: result?.blockers || [],
            result,
          };
        } else {
          this.error = error instanceof Error ? error.message : String(error);
        }
      } finally {
        // 无论成功失败都刷新：失败记录已由后端落库，刷新后可回读阻断项。
        await this.load(path);
        this.loading = false;
      }
    },
  },
});
