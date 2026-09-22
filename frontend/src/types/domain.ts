
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
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; details?: unknown; meta?: PageMeta }
export interface BerthingGateCheck { code: string; label: string; passed: boolean; message: string }
export interface BerthingGateResult {
  vesselId: number; vesselCode: string; berth: string; fromStatus: string; targetStatus: string;
  required: boolean; allowed: boolean; clearanceCode?: string; windowCode?: string; windowVersion?: number;
  checks: BerthingGateCheck[]; blockers: string[]; evaluatedAt: string;
}
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; windowVersion?: number; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
