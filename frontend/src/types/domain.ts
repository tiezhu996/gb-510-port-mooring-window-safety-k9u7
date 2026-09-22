
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
  windowVersion?: number;
  submittedBy?: string;
  submittedAt?: string;
  confirmedBy?: string;
  confirmedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }

// GateBlocker 与后端 dto.GateBlocker 对齐，code 用于稳定分支判断。
export interface GateBlocker { code: string; message: string }
export interface BerthingGateResult {
  vesselId: number;
  vesselCode: string;
  vesselStatus: string;
  facility: string;
  targetStatus: string;
  passed: boolean;
  blockers: GateBlocker[];
  clearanceCode?: string;
  windowCode?: string;
  windowVersion?: number;
  windowStatus?: string;
  checkedAt: string;
  persisted: boolean;
  actor?: string;
  requestId?: string;
}
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; windowVersion?: number; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
