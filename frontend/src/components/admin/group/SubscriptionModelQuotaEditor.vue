<template>
  <div class="space-y-2">
    <div class="flex items-center justify-between gap-3">
      <label class="input-label mb-0">{{ t('admin.groups.subscription.modelQuota') }}</label>
      <button type="button" class="btn btn-secondary h-8 px-2.5 text-xs" :disabled="rows.length >= maxRules" @click="addRow">
        <Icon name="plus" size="sm" class="mr-1" />
        {{ t('admin.groups.subscription.addModelQuota') }}
      </button>
    </div>

    <div v-for="row in rows" :key="row.id" class="grid grid-cols-[minmax(0,1fr)_7rem_2rem] items-center gap-2">
      <input v-model.trim="row.model" type="text" class="input min-w-0" placeholder="claude-fable-5" @input="emitValue" />
      <div class="relative">
        <input v-model.number="row.percent" type="number" min="0.01" max="100" step="0.01" class="input pr-7" @input="emitValue" />
        <span class="pointer-events-none absolute inset-y-0 right-2 flex items-center text-xs text-gray-400">%</span>
      </div>
      <button type="button" class="flex h-8 w-8 items-center justify-center text-gray-400 transition-colors hover:text-red-500" :title="t('common.delete')" @click="removeRow(row.id)">
        <Icon name="trash" size="sm" />
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

interface QuotaRow {
  id: number
  model: string
  percent: number
}

const maxRules = 50
const model = defineModel<Record<string, number>>({ required: true })
const { t } = useI18n()
const rows = ref<QuotaRow[]>([])
let nextID = 1

function rowsAsValue(): Record<string, number> {
  const value: Record<string, number> = {}
  for (const row of rows.value) {
    const key = row.model.trim().toLowerCase()
    const percent = Number(row.percent)
    if (key && Number.isFinite(percent)) value[key] = percent / 100
  }
  return value
}

function signature(value: Record<string, number>): string {
  return JSON.stringify(Object.entries(value || {}).sort(([a], [b]) => a.localeCompare(b)))
}

function emitValue() {
  model.value = rowsAsValue()
}

function addRow() {
  if (rows.value.length >= maxRules) return
  rows.value.push({ id: nextID++, model: '', percent: 50 })
}

function removeRow(id: number) {
  rows.value = rows.value.filter(row => row.id !== id)
  emitValue()
}

watch(model, value => {
  if (signature(value) === signature(rowsAsValue())) return
  rows.value = Object.entries(value || {}).map(([quotaModel, ratio]) => ({
    id: nextID++,
    model: quotaModel,
    percent: Number((ratio * 100).toFixed(4)),
  }))
}, { immediate: true, deep: true })
</script>
