<script setup lang="ts">
import { computed } from 'vue';
import type { BerthingGateResult, DomainRecord } from '../../types/domain';
import { useAuth } from '../../hooks/useAuth';
import StatusBadge from './StatusBadge.vue';

const props = defineProps<{
  vessels: DomainRecord[];
  results: BerthingGateResult[];
  loading?: boolean;
}>();
const emit = defineEmits<{ evaluate: [item: DomainRecord] }>();
const { session } = useAuth();
const roleRank: Record<string, number> = { viewer: 1, operator: 2, reviewer: 3, admin: 4 };
const canWrite = computed(() => (roleRank[session.value?.role || ''] || 0) >= roleRank.operator);

const resultMap = computed(() => {
  const map = new Map<number, BerthingGateResult>();
  props.results.forEach((result) => map.set(result.vesselId, result));
  return map;
});

// 只关注尚未系泊的任务：已系泊任务展示最近一次放行结果，其它任务可预检。
const rows = computed(() =>
  props.vessels
    .map((vessel) => ({ vessel, gate: resultMap.value.get(vessel.id) || null }))
    .filter(({ vessel, gate }) => vessel.status !== 'departed' || gate),
);

function tone(gate: BerthingGateResult | null, vessel: DomainRecord): 'success' | 'warning' | 'danger' | 'neutral' {
  if (vessel.status === 'moored') return 'success';
  if (!gate) return 'neutral';
  return gate.passed ? 'success' : 'danger';
}

function summary(gate: BerthingGateResult | null, vessel: DomainRecord): string {
  if (vessel.status === 'moored') return gate?.passed ? '闸门已通过并放行' : '已系泊';
  if (!gate) return '尚未核对';
  if (gate.passed) return gate.persisted ? '预检通过（放行时将复核）' : '预检通过';
  return `被阻断（${gate.blockers.length} 项）`;
}
</script>

<template>
  <section class="clearance-panel" aria-label="靠泊放行闸门">
    <header>
      <div>
        <span class="eyebrow">BERTHING RELEASE GATE</span>
        <strong>靠泊放行闸门</strong>
      </div>
      <small>推进至「已系泊」前必须存在同泊位已放行且已生效的安全许可，且固化风浪窗口处于 safe 且版本一致</small>
    </header>
    <div v-loading="loading" class="gate-table">
      <article v-for="{ vessel, gate } in rows" :key="vessel.id" class="gate-row" :class="`gate-row--${tone(gate, vessel)}`">
        <div class="gate-main">
          <div class="clearance-title">
            <strong>{{ vessel.code }}</strong>
            <StatusBadge :status="vessel.status"/>
          </div>
          <small class="muted">{{ vessel.facility }}</small>
          <p class="gate-summary">{{ summary(gate, vessel) }}</p>
          <ul v-if="gate && !gate.passed && gate.blockers.length" class="gate-blockers">
            <li v-for="blocker in gate.blockers" :key="blocker.code">
              <code>{{ blocker.code }}</code>
              <span>{{ blocker.message }}</span>
            </li>
          </ul>
          <dl v-if="gate && (gate.clearanceCode || gate.windowCode)" class="gate-context">
            <template v-if="gate.clearanceCode"><dt>安全许可</dt><dd>{{ gate.clearanceCode }}</dd></template>
            <template v-if="gate.windowCode"><dt>风浪窗口</dt><dd>{{ gate.windowCode }} · v{{ gate.windowVersion }} · {{ gate.windowStatus }}</dd></template>
            <dt>核对时间</dt><dd>{{ new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(gate.checkedAt)) }}</dd>
          </dl>
        </div>
        <div class="gate-actions">
          <el-button
            v-if="canWrite && vessel.status !== 'moored' && vessel.status !== 'departed'"
            size="small" @click="emit('evaluate', vessel)"
          >核对闸门</el-button>
          <small v-if="gate" class="muted">{{ gate.persisted ? '已留痕，可回读' : '实时预检结果' }}</small>
        </div>
      </article>
      <p v-if="rows.length === 0" class="empty">暂无需核对的靠泊任务</p>
    </div>
  </section>
</template>

<style scoped>
.gate-table { display: grid; gap: 10px; }
.gate-row { display: flex; justify-content: space-between; gap: 16px; border-left: 3px solid #b9c8d1; background: #f7fafb; padding: 13px 15px; }
.gate-row--success { border-left-color: #2a9d78; }
.gate-row--danger { border-left-color: #c84855; background: #fdf3f4; }
.gate-row--warning { border-left-color: #d9a441; }
.gate-main { min-width: 0; }
.gate-summary { margin: 6px 0 8px; font-weight: 650; font-size: 13px; }
.gate-blockers { margin: 0 0 8px; padding-left: 18px; display: grid; gap: 4px; color: #9d2c37; font-size: 12px; }
.gate-blockers code { color: #9d2c37; background: #fde4e6; padding: 1px 5px; margin-right: 6px; border-radius: 3px; }
.gate-context { display: grid; grid-template-columns: auto 1fr; gap: 4px 12px; margin: 0; font-size: 12px; }
.gate-context dt { color: #72848f; }
.gate-context dd { margin: 0; font-weight: 600; }
.gate-actions { display: grid; align-content: start; justify-items: end; gap: 6px; }
</style>
