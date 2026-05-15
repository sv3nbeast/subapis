<template>
  <div
    v-if="mode === 'checkbox' && documents.length > 0"
    class="px-0.5"
  >
    <div class="flex items-start gap-2">
      <input
        id="login-agreement-consent"
        type="checkbox"
        :checked="accepted"
        class="mt-[2px] h-4 w-4 flex-shrink-0 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-900"
        @change="handleCheckboxChange"
      />
      <div class="min-w-0 flex-1">
        <p class="text-[13px] leading-5 text-gray-600 dark:text-dark-300">
          <label
            for="login-agreement-consent"
            class="cursor-pointer text-gray-700 dark:text-dark-200"
          >
            我已阅读并同意
          </label>
          <template v-for="(doc, index) in documents" :key="doc.id || doc.title">
            <RouterLink
              :to="documentRoute(doc)"
              target="_blank"
              rel="noopener noreferrer"
              class="font-medium text-primary-600 underline-offset-4 transition hover:text-primary-700 hover:underline dark:text-primary-300 dark:hover:text-primary-200"
            >
              {{ doc.title }}
            </RouterLink>
            <span v-if="index < documents.length - 1">、</span>
          </template>
        </p>
      </div>
    </div>
  </div>

  <div
    v-else-if="!accepted && documents.length > 0"
    class="rounded-lg border border-primary-100 bg-primary-50/70 p-3 text-sm text-primary-900 dark:border-primary-500/20 dark:bg-primary-500/10 dark:text-primary-100"
  >
    <div class="flex items-start gap-3">
      <Icon name="shield" size="sm" class="mt-0.5 flex-shrink-0 text-primary-600 dark:text-primary-300" />
      <div class="min-w-0 flex-1">
        <p class="font-medium">继续登录前需要先同意最新条款。</p>
        <p class="mt-1 text-primary-700 dark:text-primary-200/80">
          未同意前，账号密码输入和快捷登录会保持禁用。
        </p>
      </div>
      <button
        type="button"
        class="flex-shrink-0 rounded-md bg-primary-600 px-3 py-1.5 text-xs font-medium text-white transition hover:bg-primary-700"
        @click="emit('open')"
      >
        查看条款
      </button>
    </div>
  </div>

  <Teleport to="body">
    <Transition name="agreement-fade">
      <div
        v-if="dialogVisible"
        class="fixed inset-0 z-[140] flex items-center justify-center overflow-y-auto bg-gray-950/55 p-4"
      >
        <div
          class="w-full max-w-[744px] rounded-[14px] bg-white px-8 py-10 shadow-[0_20px_60px_rgba(15,23,42,0.28)] ring-1 ring-gray-950/20 dark:bg-dark-900 dark:ring-white/10 sm:px-[60px] sm:py-[58px]"
        >
          <div class="mx-auto max-w-[626px]">
            <h2 class="text-[26px] font-bold leading-tight tracking-[-0.02em] text-gray-950 dark:text-white">
              条款更新通知
            </h2>
            <p class="mt-3 text-[16px] leading-7 text-gray-800 dark:text-dark-200">
              我们的服务条款已于 {{ updatedAt || '近期' }} 更新。在继续使用我们的服务之前，请仔细阅读并同意以下条款。
            </p>

            <div class="mt-7">
              <p class="text-[16px] font-medium text-gray-800 dark:text-dark-200">相关文档</p>
            </div>

            <div class="mt-4 space-y-2">
              <RouterLink
                v-for="(doc, index) in documents"
                :key="doc.id || doc.title"
                :to="documentRoute(doc)"
                target="_blank"
                rel="noopener noreferrer"
                class="group flex min-h-[58px] w-full items-center gap-4 rounded-xl px-4 text-left transition hover:bg-gray-50 dark:hover:bg-dark-800"
              >
                <span class="flex h-7 w-7 flex-shrink-0 items-center justify-center text-gray-900 transition group-hover:text-gray-950 dark:text-dark-100">
                  <Icon :name="documentIcon(index, doc.title)" size="md" :stroke-width="2.5" />
                </span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-[16px] font-medium text-gray-900 dark:text-white">{{ displayDocumentTitle(doc.title) }}</span>
                </span>
                <span class="flex h-8 w-8 flex-shrink-0 items-center justify-center text-gray-900 transition group-hover:translate-x-0.5 dark:text-dark-100">
                  <Icon name="chevronRight" size="sm" :stroke-width="2.5" />
                </span>
              </RouterLink>
            </div>

            <label class="mt-9 flex cursor-pointer items-center gap-3 text-[16px] text-gray-900 dark:text-dark-100">
              <input
                v-model="modalAgreementChecked"
                type="checkbox"
                class="h-5 w-5 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-900"
              />
              <span>我已阅读并同意上述所有条款</span>
            </label>

            <div class="mt-8 grid grid-cols-2 gap-3">
              <button
                type="button"
                class="rounded-xl bg-gray-100 px-4 py-3.5 text-[16px] font-semibold text-gray-900 transition hover:bg-gray-200 dark:bg-dark-800 dark:text-dark-100 dark:hover:bg-dark-700"
                @click="emit('reject')"
              >
                拒绝
              </button>
              <button
                type="button"
                :disabled="!modalAgreementChecked"
                class="rounded-xl bg-gray-200 px-4 py-3.5 text-[16px] font-semibold text-gray-500 transition enabled:bg-primary-600 enabled:text-white enabled:shadow-sm enabled:shadow-primary-600/20 enabled:hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-80 dark:bg-dark-800 dark:text-dark-500 dark:enabled:bg-primary-600 dark:enabled:text-white"
                @click="handleModalAccept"
              >
                同意并继续
              </button>
            </div>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import type { LoginAgreementDocument } from '@/types'

const props = withDefaults(defineProps<{
  accepted: boolean
  documents: LoginAgreementDocument[]
  mode: 'modal' | 'checkbox' | string
  updatedAt?: string
  visible: boolean
}>(), {
  updatedAt: ''
})

const emit = defineEmits<{
  accept: []
  reject: []
  open: []
}>()

const dialogVisible = computed(() => props.visible && documents.value.length > 0)
const documents = computed(() => props.documents.filter((doc) => doc.title.trim()))
const updatedAt = computed(() => props.updatedAt || '')
const accepted = computed(() => props.accepted)
const mode = computed(() => props.mode === 'checkbox' ? 'checkbox' : 'modal')
const modalAgreementChecked = ref(false)

watch(dialogVisible, (visible) => {
  if (visible) {
    modalAgreementChecked.value = false
  }
})

function documentRoute(doc: LoginAgreementDocument) {
  return {
    name: 'LegalDocument',
    params: {
      documentId: doc.id || doc.title,
    },
  }
}

function handleCheckboxChange(event: Event): void {
  const checked = (event.target as HTMLInputElement).checked
  if (checked) {
    emit('accept')
  } else {
    emit('reject')
  }
}

function handleModalAccept(): void {
  if (!modalAgreementChecked.value) {
    return
  }
  emit('accept')
}

function displayDocumentTitle(title: string): string {
  return title.split('/')[0]?.trim() || title
}

function documentIcon(index: number, title: string): 'document' | 'shield' | 'globe' | 'cog' {
  if (title.includes('政策') || title.includes('隐私')) {
    return 'shield'
  }
  if (title.includes('国家') || title.includes('地区')) {
    return 'globe'
  }
  if (index === 3) {
    return 'cog'
  }
  return 'document'
}
</script>

<style scoped>
.agreement-fade-enter-active,
.agreement-fade-leave-active {
  transition: opacity 0.18s ease;
}

.agreement-fade-enter-from,
.agreement-fade-leave-to {
  opacity: 0;
}

.agreement-fade-enter-active > div,
.agreement-fade-leave-active > div {
  transition: transform 0.18s ease, opacity 0.18s ease;
}

.agreement-fade-enter-from > div,
.agreement-fade-leave-to > div {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}
</style>
