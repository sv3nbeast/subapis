<template>
  <div ref="root" class="relative" @keydown.esc="open = false">
    <button type="button" class="wc-mchip" :disabled="disabled" :aria-expanded="open" aria-haspopup="dialog" @click="toggle">
      <span class="wc-mk" :data-brand="brandKey(selected?.brand)" aria-hidden="true">{{ mark }}</span>
      <span class="trunc">{{ selected?.name || text.choose }}</span>
      <Icon name="chevronDown" size="xs" />
    </button>
    <div v-if="open" role="dialog" :aria-label="text.choose" class="wc-pop">
      <input ref="searchInput" v-model="query" :aria-label="text.search" :placeholder="text.search" class="wc-input" />
      <div class="wc-pop-list">
        <template v-for="section in sections" :key="section.brand">
          <p class="wc-pop-brand">{{ section.brand }}</p>
          <button v-for="item in section.items" :key="item.id" type="button" class="wc-pop-item" :aria-pressed="item.id === modelValue" @click="choose(item.id)">
            <span class="wc-mk" :data-brand="brandKey(item.brand)" aria-hidden="true">{{ (item.brand || item.name).slice(0, 1).toUpperCase() }}</span>
            <span class="min-w-0 flex-1">
              <span class="n">{{ item.name }}<em v-if="item.recommended">{{ text.recommended }}</em></span>
              <span v-if="item.description" class="d">{{ item.description }}</span>
              <span class="b">{{ item.billing_type === 'subscription' ? text.subscription : text.balance }}</span>
            </span>
            <Icon v-if="item.id === modelValue" name="check" size="sm" class="flex-none" style="color: var(--wc-acc-d)" />
          </button>
        </template>
        <p v-if="!sections.length" class="wc-pop-empty">{{ text.empty }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onClickOutside } from '@vueuse/core'
import Icon from '@/components/icons/Icon.vue'
import type { WebChatCatalogOption } from '@/api/webChat'
import { brandKey } from '@/utils/webChatFormat'

const props = defineProps<{ modelValue: string; models: WebChatCatalogOption[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [string] }>()
const { locale } = useI18n()
const text = computed(() => locale.value.startsWith('zh')
  ? { choose: '选择模型', search: '搜索模型', recommended: '推荐', subscription: '使用订阅额度', balance: '使用余额 · 按量计费', empty: '暂无可用模型，请联系管理员' }
  : { choose: 'Choose a model', search: 'Search models', recommended: 'Recommended', subscription: 'Subscription quota', balance: 'Balance · pay as you go', empty: 'No models available. Contact your administrator.' })
const root = ref<HTMLElement | null>(null), searchInput = ref<HTMLInputElement | null>(null), open = ref(false), query = ref('')
const selected = computed(() => props.models.find(model => model.id === props.modelValue))
const mark = computed(() => (selected.value?.brand || selected.value?.name || '?').slice(0, 1).toUpperCase())
const sections = computed(() => {
  const groups = new Map<string, WebChatCatalogOption[]>()
  for (const model of props.models) {
    if (!`${model.name} ${model.model} ${model.brand}`.toLowerCase().includes(query.value.toLowerCase())) continue
    const brand = model.brand || 'Models'
    groups.set(brand, [...(groups.get(brand) || []), model])
  }
  return [...groups].map(([brand, items]) => ({ brand, items }))
})
onClickOutside(root, () => { open.value = false })
async function toggle() {
  open.value = !open.value
  if (open.value) { query.value = ''; await nextTick(); searchInput.value?.focus() }
}
function choose(id: string) {
  emit('update:modelValue', id)
  open.value = false
  root.value?.querySelector('button')?.focus()
}
</script>
