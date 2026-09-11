<template>
  <section class="workspace-home wc-home" aria-labelledby="workspace-welcome">
    <p class="welcome-name hello" v-if="name">{{ t('workspace.hello', { name }) }}</p>
    <h1 id="workspace-welcome">{{ t('workspace.startTitle') }}</h1>
    <p class="welcome-hint hint">{{ t('workspace.startHint') }}</p>
    <slot name="composer" />
    <div class="task-shortcuts wc-shortcuts">
      <button v-for="item in shortcuts" :key="item.key" :disabled="disabled" @click="$emit('shortcut', item.key)">
        <Icon :name="item.icon" size="sm" /> {{ t('workspace.' + item.key) }} <Icon name="chevronRight" size="xs" />
      </button>
    </div>
    <div class="recent-work wc-recent" v-if="sessions.length">
      <h2>{{ t('workspace.recent') }}</h2>
      <button v-for="session in sessions.slice(0, 5)" :key="session.id" :disabled="disabled" @click="$emit('select', session)">
        <Icon name="chat" size="sm" />
        <span>{{ session.title || session.model }}</span>
        <time :datetime="session.updated_at">{{ date(session.updated_at) }}</time>
        <Icon name="chevronRight" size="xs" />
      </button>
    </div>
  </section>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { WebChatSession } from '@/api/webChat'
defineProps<{ name?: string; sessions: WebChatSession[]; disabled: boolean }>()
defineEmits<{ select: [session: WebChatSession]; shortcut: [kind: string] }>()
const { t, locale } = useI18n()
const shortcuts = [{ key: 'slides', icon: 'book' }, { key: 'analysis', icon: 'chart' }, { key: 'document', icon: 'edit' }] as const
function date(value: string) { const d = new Date(value); return Number.isNaN(d.getTime()) ? '' : new Intl.DateTimeFormat(locale.value, {month:'short',day:'numeric'}).format(d) }
</script>
<style scoped>
.workspace-home{width:min(100%,58rem);margin:0 auto;padding:clamp(2rem,7vh,5rem) 1.5rem 2rem}.welcome-name{color:var(--wa-muted);font-size:1rem;margin-bottom:.6rem}.workspace-home h1{font-size:clamp(1.75rem,3vw,2.5rem);letter-spacing:-.04em;line-height:1.25;font-weight:700}.welcome-hint{margin:.8rem 0 1.5rem;color:var(--wa-muted)}
.task-shortcuts{display:flex;flex-wrap:wrap;gap:.6rem;margin:1rem 0 2.5rem}.task-shortcuts button{display:flex;gap:.55rem;align-items:center;border:1px solid var(--wa-line);border-radius:6px;padding:.35rem .65rem;font-size:.875rem}.task-shortcuts button:hover{background:var(--wa-soft)}
.recent-work h2{font-size:1rem;font-weight:650;margin-bottom:.75rem}.recent-work>button{display:flex;align-items:center;gap:.75rem;border-top:1px solid var(--wa-line);width:100%;padding:.85rem .25rem;text-align:left;font-size:.875rem}.recent-work>button:hover{background:var(--wa-soft)}.recent-work span{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.recent-work time{color:var(--wa-muted);font-size:.8125rem}
@media(max-width:767px){.workspace-home{padding:2rem 1rem}.task-shortcuts button{min-height:44px}.recent-work time{display:none}}
</style>
