<script setup lang="ts">
import EntityPage from '../components/EntityPage.vue';
import BerthingGatePanel from '../components/common/BerthingGatePanel.vue';
import type { DomainRecord } from '../types/domain';
import { ENTITY_CONFIGS } from '../types/status';
import { useVesselCallStore } from '../stores/vessel-call';

const store = useVesselCallStore();
const evaluateGate = (item: DomainRecord) => store.evaluateGate(item);
</script>

<template>
  <EntityPage :config="ENTITY_CONFIGS[0]" :store="store">
    <template #insight>
      <el-alert
        v-if="store.gateFailure"
        :title="`靠泊放行闸门未通过，任务保持原状态：${store.gateFailure.message}`"
        type="error" show-icon :closable="false" class="gate-failure-alert"
      >
        <ul class="gate-failure-list">
          <li v-for="blocker in store.gateFailure.blockers" :key="blocker.code">
            <code>{{ blocker.code }}</code> {{ blocker.message }}
          </li>
        </ul>
      </el-alert>
      <BerthingGatePanel
        :vessels="store.items"
        :results="store.gateResults"
        :loading="store.gateLoading"
        @evaluate="evaluateGate"
      />
    </template>
  </EntityPage>
</template>

<style scoped>
.gate-failure-alert { margin-bottom: 14px; }
.gate-failure-list { margin: 8px 0 0; padding-left: 18px; display: grid; gap: 4px; }
.gate-failure-list code { background: rgba(200, 76, 85, 0.15); padding: 1px 5px; border-radius: 3px; }
</style>
