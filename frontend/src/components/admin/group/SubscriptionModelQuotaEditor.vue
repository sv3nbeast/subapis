<template>
  <div class="space-y-4">
    <div class="space-y-2">
      <div class="flex items-center justify-between gap-3">
        <label class="input-label mb-0">{{ t('admin.groups.subscription.modelQuota') }}</label>
        <button type="button" class="btn btn-secondary h-8 px-2.5 text-xs" :disabled="rows.length >= maxRules" @click="addRow">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('admin.groups.subscription.addModelQuota') }}
        </button>
      </div>

      <div v-for="row in rows" :key="row.id" class="grid grid-cols-[minmax(0,1fr)_7rem_2rem] items-center gap-2">
        <input v-model.trim="row.model" type="text" class="input min-w-0" placeholder="claude-fable-5" @input="emitRatios" />
        <div class="relative">
          <input v-model.number="row.percent" type="number" min="0.01" max="100" step="0.01" class="input pr-7" @input="emitRatios" />
          <span class="pointer-events-none absolute inset-y-0 right-2 flex items-center text-xs text-gray-400">%</span>
        </div>
        <button type="button" class="flex h-8 w-8 items-center justify-center text-gray-400 transition-colors hover:text-red-500" :title="t('common.delete')" @click="removeRow(row.id)">
          <Icon name="trash" size="sm" />
        </button>
      </div>
    </div>

    <div class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-600">
      <div class="flex items-center justify-between gap-3">
        <label class="input-label mb-0">{{ t('admin.groups.subscription.sharedModelQuota') }}</label>
        <button type="button" class="btn btn-secondary h-8 px-2.5 text-xs" :disabled="groupRows.length >= maxRules" @click="addGroup">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('admin.groups.subscription.addSharedModelQuota') }}
        </button>
      </div>

      <div v-for="group in groupRows" :key="group.localID" class="space-y-2 border-b border-gray-200 pb-3 last:border-b-0 dark:border-dark-600">
        <div class="grid grid-cols-[minmax(0,1fr)_7rem_2rem] items-center gap-2">
          <input v-model.trim="group.name" type="text" class="input min-w-0" :placeholder="t('admin.groups.subscription.sharedModelQuotaName')" @input="emitGroups" />
          <div class="relative">
            <input v-model.number="group.percent" type="number" min="0.01" max="100" step="0.01" class="input pr-7" @input="emitGroups" />
            <span class="pointer-events-none absolute inset-y-0 right-2 flex items-center text-xs text-gray-400">%</span>
          </div>
          <button type="button" class="flex h-8 w-8 items-center justify-center text-gray-400 transition-colors hover:text-red-500" :title="t('common.delete')" @click="removeGroup(group.localID)">
            <Icon name="trash" size="sm" />
          </button>
        </div>
        <div v-for="member in group.models" :key="member.id" class="grid grid-cols-[minmax(0,1fr)_2rem] items-center gap-2 pl-4">
          <input v-model.trim="member.model" type="text" class="input min-w-0" placeholder="claude-fable-5-1" @input="emitGroups" />
          <button type="button" class="flex h-8 w-8 items-center justify-center text-gray-400 transition-colors hover:text-red-500 disabled:opacity-30" :title="t('common.delete')" :disabled="group.models.length <= 2" @click="removeGroupModel(group.localID, member.id)">
            <Icon name="trash" size="sm" />
          </button>
        </div>
        <button type="button" class="btn btn-secondary ml-4 h-8 px-2.5 text-xs" :disabled="group.models.length >= maxGroupModels" @click="addGroupModel(group.localID)">
          <Icon name="plus" size="sm" class="mr-1" />
          {{ t('admin.groups.subscription.addModelQuota') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { SubscriptionModelQuotaGroup } from '@/types'

interface QuotaRow {
  id: number
  model: string
  percent: number
}

interface GroupModelRow {
  id: number
  model: string
}

interface QuotaGroupRow {
  localID: number
  id: string
  name: string
  models: GroupModelRow[]
  percent: number
}

const maxRules = 50
const maxGroupModels = 50
const model = defineModel<Record<string, number>>({ required: true })
const groups = defineModel<SubscriptionModelQuotaGroup[]>('groups', { default: () => [] })
const { t } = useI18n()
const rows = ref<QuotaRow[]>([])
const groupRows = ref<QuotaGroupRow[]>([])
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

function groupsAsValue(): SubscriptionModelQuotaGroup[] {
  return groupRows.value.map(group => ({
    id: group.id,
    name: group.name.trim(),
    models: group.models.map(member => member.model.trim().toLowerCase()).filter(Boolean),
    ratio: Number(group.percent) / 100,
  }))
}

function signature(value: unknown): string {
  return JSON.stringify(value || {})
}

function emitRatios() {
  model.value = rowsAsValue()
}

function emitGroups() {
  groups.value = groupsAsValue()
}

function addRow() {
  if (rows.value.length >= maxRules) return
  rows.value.push({ id: nextID++, model: '', percent: 50 })
}

function removeRow(id: number) {
  rows.value = rows.value.filter(row => row.id !== id)
  emitRatios()
}

function addGroup() {
  if (groupRows.value.length >= maxRules) return
  const localID = nextID++
  groupRows.value.push({
    localID,
    id: `shared-${Date.now().toString(36)}-${localID}`,
    name: '',
    models: [{ id: nextID++, model: '' }, { id: nextID++, model: '' }],
    percent: 50,
  })
  emitGroups()
}

function removeGroup(localID: number) {
  groupRows.value = groupRows.value.filter(group => group.localID !== localID)
  emitGroups()
}

function addGroupModel(localID: number) {
  const group = groupRows.value.find(item => item.localID === localID)
  if (!group || group.models.length >= maxGroupModels) return
  group.models.push({ id: nextID++, model: '' })
}

function removeGroupModel(localID: number, memberID: number) {
  const group = groupRows.value.find(item => item.localID === localID)
  if (!group || group.models.length <= 2) return
  group.models = group.models.filter(member => member.id !== memberID)
  emitGroups()
}

watch(model, value => {
  if (signature(value) === signature(rowsAsValue())) return
  rows.value = Object.entries(value || {}).map(([quotaModel, ratio]) => ({
    id: nextID++,
    model: quotaModel,
    percent: Number((ratio * 100).toFixed(4)),
  }))
}, { immediate: true, deep: true })

watch(groups, value => {
  if (signature(value) === signature(groupsAsValue())) return
  groupRows.value = (value || []).map(group => ({
    localID: nextID++,
    id: group.id,
    name: group.name,
    models: group.models.map(quotaModel => ({ id: nextID++, model: quotaModel })),
    percent: Number((group.ratio * 100).toFixed(4)),
  }))
}, { immediate: true, deep: true })
</script>
