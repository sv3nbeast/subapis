<template>
  <div class="web-agent-workbench">
    <a class="skip-content" href="#web-agent-main">{{ t('workspace.skip') }}</a>
    <header class="workbench-bar">
      <button class="workbench-brand" :disabled="busy" @click="$emit('home')">SubAPIs</button>
      <nav :aria-label="t('workspace.navigation')">
        <button :class="{ active: section === 'chat' }" :disabled="busy" @click="$emit('home')">{{ t('workspace.home') }}</button>
        <button v-if="projectsEnabled" :class="{ active: section === 'projects' }" :disabled="busy" @click="$emit('navigate', 'projects')">{{ t('webChat.projects') }}</button>
        <button v-if="artifactsEnabled || (filesEnabled && projectsEnabled)" :class="{ active: section === 'files' }" :disabled="busy" @click="$emit('navigate', 'files')">{{ t('workspace.files') }}</button>
        <button v-if="templatesEnabled" :disabled="busy" @click="$emit('templates')">{{ t('workspace.assistants') }}</button>
      </nav>
      <RouterLink class="console-link" to="/dashboard">{{ t('workspace.console') }} <Icon name="arrowRight" size="sm" /></RouterLink>
    </header>
    <slot />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
defineProps<{ section: string; busy: boolean; projectsEnabled: boolean; filesEnabled: boolean; templatesEnabled: boolean; artifactsEnabled?: boolean }>()
defineEmits<{ home: []; navigate: [section: 'projects' | 'files']; templates: [] }>()
const { t } = useI18n()
</script>

<style>
.web-agent-workbench{--wa-bg:#fff;--wa-soft:#f7f8fa;--wa-text:#18181b;--wa-muted:#71717a;--wa-line:#e6e7eb;--wa-active:#eff6ff;--wa-accent:#2563eb;background:var(--wa-bg);color:var(--wa-text);height:100dvh;display:flex;flex-direction:column;font:1rem/1.6 system-ui,sans-serif}
.dark .web-agent-workbench{--wa-bg:#18181b;--wa-soft:#222226;--wa-text:#f4f4f5;--wa-muted:#a1a1aa;--wa-line:#343438;--wa-active:#1e293b;--wa-accent:#60a5fa}
.workbench-bar{height:3.5rem;flex-shrink:0;display:flex;align-items:center;padding:0 1.25rem;border-bottom:1px solid var(--wa-line);gap:2rem}
.workbench-brand{font-size:1.25rem;font-weight:750;letter-spacing:-.04em;min-width:12.5rem;text-align:left}
.workbench-bar nav{display:flex;height:100%;gap:1.5rem;align-items:center}.workbench-bar nav button{font-size:.875rem;height:100%;border-bottom:2px solid transparent}.workbench-bar nav button.active{border-color:var(--wa-accent);font-weight:650}
.console-link{margin-left:auto;display:flex;align-items:center;gap:.4rem;color:var(--wa-muted);font-size:.875rem}
.web-agent-workbench :is(button,a,input,select,textarea):focus-visible{outline:2px solid var(--wa-accent);outline-offset:3px}
.web-agent-workbench button:disabled{opacity:.45;cursor:not-allowed}.skip-content{position:fixed;left:1rem;top:-5rem;z-index:100;padding:.5rem;background:var(--wa-bg)}.skip-content:focus{top:.5rem}
@media(max-width:767px){.workbench-bar{gap:1rem;padding:0 1rem}.workbench-brand{min-width:0}.workbench-bar nav{gap:.9rem}.console-link{font-size:0}.workbench-bar nav button{min-height:44px}.web-agent-workbench button{touch-action:manipulation}}
@media(prefers-reduced-motion:reduce){.web-agent-workbench *, .web-agent-workbench *::before{transition:none!important;animation:none!important;scroll-behavior:auto!important}}
</style>
