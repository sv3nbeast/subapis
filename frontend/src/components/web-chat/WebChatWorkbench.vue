<template>
  <div class="web-agent-workbench">
    <a class="skip-content" href="#web-agent-main">{{ t('workspace.skip') }}</a>
    <header class="workbench-bar wc-wbar">
      <button class="workbench-brand wc-brand" :disabled="busy" @click="$emit('home')"><span class="mark">SA</span><span>SubAPIs</span></button>
      <nav class="wc-nav" :aria-label="t('workspace.navigation')">
        <button :class="{ active: section === 'chat' }" :disabled="busy" @click="$emit('home')"><Icon name="chat" size="sm" /><span>{{ t('workspace.home') }}</span></button>
        <button v-if="projectsEnabled" :class="{ active: section === 'projects' }" :disabled="busy" @click="$emit('navigate', 'projects')"><Icon name="folder" size="sm" /><span>{{ t('webChat.projects') }}</span></button>
        <button v-if="artifactsEnabled || (filesEnabled && projectsEnabled)" :class="{ active: section === 'files' }" :disabled="busy" @click="$emit('navigate', 'files')"><Icon name="layers" size="sm" /><span>{{ t('workspace.files') }}</span></button>
        <button v-if="templatesEnabled" :disabled="busy" @click="$emit('templates')"><Icon name="sparkles" size="sm" /><span>{{ t('workspace.assistants') }}</span></button>
      </nav>
      <div class="wc-right">
        <slot name="usage" />
        <span v-if="balance" class="wc-usage"><Icon name="dollar" size="xs" /><b>{{ balance }}</b></span>
        <RouterLink class="console-link wc-tb wc-tb-o" to="/dashboard">{{ t('workspace.console') }}<Icon name="chevronRight" size="xs" /></RouterLink>
        <span v-if="userName" class="wc-who" :title="userName">{{ userName.slice(0, 1).toUpperCase() }}</span>
      </div>
    </header>
    <slot />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
defineProps<{ section: string; busy: boolean; projectsEnabled: boolean; filesEnabled: boolean; templatesEnabled: boolean; artifactsEnabled?: boolean; balance?: string; userName?: string }>()
defineEmits<{ home: []; navigate: [section: 'projects' | 'files']; templates: [] }>()
const { t } = useI18n()
</script>
