<template>
  <Teleport to="body">
    <Transition name="public-dialog">
      <div
        v-if="open"
        class="public-announcement-backdrop"
        role="presentation"
        @mousedown.self="$emit('close')"
      >
        <section
          ref="dialogRef"
          class="public-announcement-dialog"
          role="dialog"
          aria-modal="true"
          aria-labelledby="public-announcement-title"
          tabindex="-1"
        >
          <header>
            <div>
              <span class="public-announcement-icon" aria-hidden="true">
                <Icon name="bell" size="md" />
              </span>
              <div>
                <h2 id="public-announcement-title">{{ t('home.announcements.modalTitle') }}</h2>
                <p>{{ t('home.announcements.modalDescription') }}</p>
              </div>
            </div>
            <button
              ref="closeButtonRef"
              type="button"
              :aria-label="t('common.close')"
              @click="$emit('close')"
            >
              <Icon name="x" size="sm" />
            </button>
          </header>

          <div class="public-announcement-tabs" role="tablist">
            <button
              id="public-announcement-notifications-tab"
              type="button"
              role="tab"
              :aria-selected="tab === 'notifications'"
              aria-controls="public-announcement-panel"
              :tabindex="tab === 'notifications' ? 0 : -1"
              :class="{ 'is-active': tab === 'notifications' }"
              @click="$emit('update:tab', 'notifications')"
              @keydown="handleTabKeydown"
            >
              {{ t('home.announcements.notifications') }}
            </button>
            <button
              id="public-announcement-system-tab"
              type="button"
              role="tab"
              :aria-selected="tab === 'system'"
              aria-controls="public-announcement-panel"
              :tabindex="tab === 'system' ? 0 : -1"
              :class="{ 'is-active': tab === 'system' }"
              @click="$emit('update:tab', 'system')"
              @keydown="handleTabKeydown"
            >
              {{ t('home.announcements.system') }}
            </button>
          </div>

          <div
            id="public-announcement-panel"
            class="public-announcement-body"
            role="tabpanel"
            :aria-labelledby="tab === 'notifications'
              ? 'public-announcement-notifications-tab'
              : 'public-announcement-system-tab'"
          >
            <div v-if="loading" class="public-announcement-loading" role="status">
              <span></span>
              <p>{{ t('common.loading') }}</p>
            </div>

            <ol v-else-if="items.length" class="public-announcement-list">
              <li v-for="item in items" :key="item.id">
                <span class="public-announcement-dot" :class="{ 'is-unread': isUnread(item) }"></span>
                <article>
                  <div>
                    <h3>{{ item.title }}</h3>
                    <span v-if="isUnread(item)">{{ t('announcements.unread') }}</span>
                  </div>
                  <p>{{ plainContent(item.content) }}</p>
                  <time>{{ formatTime(item.created_at) }}</time>
                </article>
              </li>
            </ol>

            <div v-else class="public-announcement-empty">
              <Icon name="inbox" size="xl" />
              <h3>{{ t('home.announcements.empty') }}</h3>
              <p>{{ t('home.announcements.emptyDescription') }}</p>
            </div>
          </div>

          <footer>
            <button type="button" class="btn btn-secondary" @click="$emit('close-today')">
              {{ t('home.announcements.todayClose') }}
            </button>
            <button type="button" class="btn btn-primary" @click="$emit('close')">
              {{ t('home.announcements.closeAnnouncement') }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatRelativeWithDateTime } from '@/utils/format'
import type { UserAnnouncement } from '@/types'

const props = defineProps<{
  open: boolean
  loading: boolean
  items: UserAnnouncement[]
  tab: 'notifications' | 'system'
  isAuthenticated: boolean
}>()

const emit = defineEmits<{
  close: []
  'close-today': []
  'update:tab': [value: 'notifications' | 'system']
}>()

const { t } = useI18n()
const dialogRef = ref<HTMLElement | null>(null)
const closeButtonRef = ref<HTMLButtonElement | null>(null)
let returnFocus: HTMLElement | null = null

function plainContent(content: string): string {
  return content
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/!\[[^\]]*]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)]\([^)]*\)/g, '$1')
    .replace(/[#>*_~\-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}

function formatTime(date: string): string {
  return formatRelativeWithDateTime(date)
}

function isUnread(item: UserAnnouncement): boolean {
  return props.isAuthenticated && !item.read_at
}

function handleKeydown(event: KeyboardEvent) {
  if (!props.open) return
  if (event.key === 'Escape') {
    event.preventDefault()
    emit('close')
    return
  }
  if (event.key !== 'Tab' || !dialogRef.value) return

  const focusable = Array.from(dialogRef.value.querySelectorAll<HTMLElement>(
    'button:not([disabled]), a[href], input:not([disabled]), [tabindex]:not([tabindex="-1"])',
  ))
  if (!focusable.length) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first?.focus()
  }
}

function handleTabKeydown(event: KeyboardEvent) {
  if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
  event.preventDefault()
  const nextTab = props.tab === 'notifications' ? 'system' : 'notifications'
  emit('update:tab', nextTab)
  void nextTick(() => {
    document.getElementById(`public-announcement-${nextTab}-tab`)?.focus()
  })
}

watch(() => props.open, (open) => {
  if (open) {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    document.documentElement.style.overflow = 'hidden'
    document.addEventListener('keydown', handleKeydown)
    void nextTick(() => closeButtonRef.value?.focus())
    return
  }

  document.documentElement.style.overflow = ''
  document.removeEventListener('keydown', handleKeydown)
  returnFocus?.focus()
  returnFocus = null
})

onBeforeUnmount(() => {
  document.documentElement.style.overflow = ''
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<style scoped>
.public-announcement-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: grid;
  place-items: center;
  padding: 20px;
  background: rgba(0, 0, 0, 0.32);
}

.public-announcement-dialog {
  display: grid;
  width: min(100%, 660px);
  max-height: min(760px, calc(100dvh - 40px));
  grid-template-rows: auto auto minmax(0, 1fr) auto;
  overflow: hidden;
  background: var(--ui2-surface, #fff);
  border: 1px solid var(--ui2-line, rgba(0, 0, 0, 0.1));
  border-radius: 8px;
  box-shadow: 0 24px 80px rgba(0, 0, 0, 0.24);
  color: var(--ui2-text, #1d1d1f);
  outline: none;
}

:global(.dark .public-announcement-dialog) {
  --ui2-surface: #29292e;
  --ui2-surface-muted: #232326;
  --ui2-surface-hover: #323237;
  --ui2-text: #f4f4f6;
  --ui2-text-secondary: #c5c5ca;
  --ui2-text-tertiary: #94949c;
  --ui2-line: rgba(255, 255, 255, 0.1);
  --ui2-line-strong: rgba(255, 255, 255, 0.18);
  --ui2-accent: #409cff;
  --ui2-accent-soft: rgba(64, 156, 255, 0.15);
}

.public-announcement-dialog > header,
.public-announcement-dialog > footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 16px 18px;
}

.public-announcement-dialog > header {
  border-bottom: 1px solid var(--ui2-line, rgba(0, 0, 0, 0.1));
}

.public-announcement-dialog > header > div {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 12px;
}

.public-announcement-dialog h2,
.public-announcement-dialog h3,
.public-announcement-dialog p {
  margin: 0;
  letter-spacing: 0;
}

.public-announcement-dialog h2 {
  font-size: 17px;
  font-weight: 680;
}

.public-announcement-dialog header p {
  margin-top: 3px;
  color: var(--ui2-text-secondary, #68686e);
  font-size: 12px;
}

.public-announcement-icon,
.public-announcement-dialog > header > button {
  display: grid;
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  place-items: center;
  border-radius: 8px;
}

.public-announcement-icon {
  background: var(--ui2-accent-soft, rgba(8, 122, 245, 0.1));
  color: var(--ui2-accent, #087af5);
}

.public-announcement-dialog > header > button {
  color: var(--ui2-text-secondary, #68686e);
  transition: background-color 140ms ease-out, transform 90ms ease-out;
}

.public-announcement-dialog > header > button:hover {
  background: var(--ui2-surface-hover, #f0f1f3);
}

.public-announcement-dialog > header > button:active {
  transform: scale(0.94);
}

.public-announcement-dialog :where(button):focus-visible {
  outline: 2px solid var(--ui2-accent, #087af5);
  outline-offset: 2px;
}

.public-announcement-tabs {
  display: flex;
  gap: 4px;
  padding: 10px 18px;
  background: var(--ui2-surface-muted, #f7f7f8);
  border-bottom: 1px solid var(--ui2-line, rgba(0, 0, 0, 0.1));
}

.public-announcement-tabs button {
  min-height: 32px;
  padding: 0 12px;
  border-radius: 7px;
  color: var(--ui2-text-secondary, #68686e);
  font-size: 12px;
  font-weight: 600;
}

.public-announcement-tabs button.is-active {
  background: var(--ui2-surface, #fff);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
  color: var(--ui2-text, #1d1d1f);
}

.public-announcement-body {
  min-height: 280px;
  overflow-y: auto;
  padding: 10px 18px 18px;
}

.public-announcement-loading,
.public-announcement-empty {
  display: grid;
  min-height: 280px;
  place-items: center;
  align-content: center;
  gap: 10px;
  color: var(--ui2-text-tertiary, #8b8b92);
  text-align: center;
}

.public-announcement-loading span {
  width: 28px;
  height: 28px;
  border: 2px solid var(--ui2-line-strong, rgba(0, 0, 0, 0.16));
  border-top-color: var(--ui2-accent, #087af5);
  border-radius: 50%;
  animation: public-announcement-spin 800ms linear infinite;
}

.public-announcement-empty h3 {
  color: var(--ui2-text, #1d1d1f);
  font-size: 15px;
}

.public-announcement-empty p,
.public-announcement-loading p {
  font-size: 12px;
}

.public-announcement-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.public-announcement-list li {
  display: grid;
  grid-template-columns: 12px minmax(0, 1fr);
  gap: 10px;
  padding: 16px 0;
  border-bottom: 1px solid var(--ui2-line, rgba(0, 0, 0, 0.1));
}

.public-announcement-list li:last-child {
  border-bottom: 0;
}

.public-announcement-dot {
  width: 7px;
  height: 7px;
  margin-top: 6px;
  background: var(--ui2-text-tertiary, #8b8b92);
  border-radius: 50%;
}

.public-announcement-dot.is-unread {
  background: var(--ui2-accent, #087af5);
  box-shadow: 0 0 0 4px var(--ui2-accent-soft, rgba(8, 122, 245, 0.1));
}

.public-announcement-list article > div {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.public-announcement-list h3 {
  color: var(--ui2-text, #1d1d1f);
  font-size: 14px;
  font-weight: 650;
}

.public-announcement-list article > div > span {
  color: var(--ui2-accent, #087af5);
  font-size: 11px;
  font-weight: 600;
}

.public-announcement-list article > p {
  margin-top: 6px;
  color: var(--ui2-text-secondary, #68686e);
  font-size: 12px;
  line-height: 1.6;
}

.public-announcement-list time {
  display: block;
  margin-top: 8px;
  color: var(--ui2-text-tertiary, #8b8b92);
  font-size: 11px;
}

.public-announcement-dialog > footer {
  justify-content: flex-end;
  background: var(--ui2-surface-muted, #f7f7f8);
  border-top: 1px solid var(--ui2-line, rgba(0, 0, 0, 0.1));
}

.public-announcement-dialog > footer .btn {
  min-height: 36px;
  border-radius: 8px;
}

.public-announcement-dialog > footer .btn-primary {
  background: var(--ui2-accent, #087af5) !important;
  border-color: transparent !important;
  box-shadow: 0 1px 2px rgba(0, 70, 150, 0.18) !important;
  color: #fff !important;
}

.public-dialog-enter-active,
.public-dialog-leave-active {
  transition: opacity 180ms ease-out;
}

.public-dialog-enter-active .public-announcement-dialog,
.public-dialog-leave-active .public-announcement-dialog {
  transition: opacity 180ms ease-out, transform 220ms cubic-bezier(0.22, 1, 0.36, 1);
}

.public-dialog-enter-from,
.public-dialog-leave-to,
.public-dialog-enter-from .public-announcement-dialog,
.public-dialog-leave-to .public-announcement-dialog {
  opacity: 0;
}

.public-dialog-enter-from .public-announcement-dialog,
.public-dialog-leave-to .public-announcement-dialog {
  transform: translateY(10px) scale(0.985);
}

@keyframes public-announcement-spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 640px) {
  .public-announcement-backdrop {
    align-items: end;
    padding: 0;
  }

  .public-announcement-dialog {
    width: 100%;
    max-height: calc(100dvh - 20px);
    border-radius: 8px 8px 0 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .public-dialog-enter-active .public-announcement-dialog,
  .public-dialog-leave-active .public-announcement-dialog {
    transition: opacity 140ms ease-out;
  }

  .public-dialog-enter-from .public-announcement-dialog,
  .public-dialog-leave-to .public-announcement-dialog {
    transform: none;
  }
}
</style>
