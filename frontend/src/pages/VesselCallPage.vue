<script setup lang="ts">
import EntityPage from '../components/EntityPage.vue';
import BerthingGatePanel from '../components/common/BerthingGatePanel.vue';
import { ENTITY_CONFIGS } from '../types/status';
import { useVesselCallStore } from '../stores/vessel-call';

const store = useVesselCallStore();
</script>

<template>
  <EntityPage :config="ENTITY_CONFIGS[0]" :store="store">
    <template #insight><BerthingGatePanel :records="store.items"/></template>
  </EntityPage>
</template>
