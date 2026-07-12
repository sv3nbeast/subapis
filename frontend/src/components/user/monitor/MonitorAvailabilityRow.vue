<template>
  <MonitorAvailabilityMeter
    class="mt-3"
    :label="windowLabel"
    :value="value"
    :note="samplesLabel"
  />
</template>

<script setup lang="ts">
import MonitorAvailabilityMeter from './MonitorAvailabilityMeter.vue'

defineProps<{
  windowLabel: string
  value: number | null
  samplesLabel?: string
}>()
</script>
