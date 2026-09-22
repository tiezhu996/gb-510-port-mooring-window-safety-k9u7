<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { BerthingGateCheck, BerthingGateResult, DomainRecord } from '../../types/domain';
import { loadBerthingGate } from '../../api/vessel-call';
import { formatDate } from '../../utils/format';

const props = defineProps<{ records: DomainRecord[] }>();
const results = ref<Record<number, BerthingGateResult>>({});
const loading = ref(false);
const loadError = ref('');

const candidates = computed(() => props.records.filter((item) => ['planned', 'approach'].includes(item.status)));
const signature = computed(() => candidates.value.map((item) => `${item.id}:${item.status}:${item.updatedAt}`).join('|'));

async function refresh() {
  loading.value = true;
  loadError.value = '';
  try {
    const entries = await Promise.all(candidates.value.map(async (item) => {
      const envelope = await loadBerthingGate(item.id);
      return [item.id, envelope.data] as const;
    }));
    results.value = Object.fromEntries(entries);
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : String(error);
  } finally {
    loading.value = false;
  }
}

watch(signature, () => void refresh(), { immediate: true });

function resultFor(id: number): BerthingGateResult | undefined {
  return results.value[id];
}
function isAllowed(id: number): boolean {
  return results.value[id]?.allowed ?? false;
}
function checksOf(id: number): BerthingGateCheck[] {
  return results.value[id]?.checks ?? [];
}
function evaluatedAt(id: number): string {
  return results.value[id]?.evaluatedAt ?? '';
}
</script>

<template>
  <section class="clearance-panel gate-panel" aria-label="靠泊放行闸门">
    <header>
      <div><span class="eyebrow">BERTHING GATE</span><strong>靠泊放行闸门</strong></div>
      <small>推进至已系泊前核对同泊位许可、风浪窗口与版本</small>
    </header>
    <el-alert v-if="loadError" :title="loadError" type="error" show-icon :closable="false"/>
    <p v-else-if="!candidates.length" class="muted">当前没有计划或进港中的靠泊任务。</p>
    <div v-else v-loading="loading" class="clearance-grid">
      <article v-for="item in candidates" :key="item.id" :class="{ 'gate-blocked': resultFor(item.id) && !isAllowed(item.id) }">
        <div class="clearance-title">
          <strong>{{ item.code }}</strong>
          <el-tag :type="isAllowed(item.id) ? 'success' : 'danger'" size="small" effect="dark">
            {{ isAllowed(item.id) ? '闸门通过' : '暂缓放行' }}
          </el-tag>
        </div>
        <p>{{ item.name }} · {{ item.facility }}</p>
        <template v-if="resultFor(item.id)">
          <ul class="gate-checks">
            <li v-for="check in checksOf(item.id)" :key="check.code" :class="check.passed ? 'gate-pass' : 'gate-fail'">
              <span>{{ check.passed ? '✓' : '✗' }} {{ check.label }}</span>
              <small>{{ check.message }}</small>
            </li>
          </ul>
          <small class="muted">评估时间 {{ formatDate(evaluatedAt(item.id)) }}</small>
        </template>
        <small v-else class="muted">正在读取闸门检查结果…</small>
      </article>
    </div>
  </section>
</template>
