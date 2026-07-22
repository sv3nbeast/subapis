<template>
  <div v-if="isPublicUiV2" class="public-ui-v2">
    <PublicHeader v-if="showChrome">
      <template #actions>
        <slot name="header-actions" />
      </template>
    </PublicHeader>
    <div role="main" :class="['public-ui-v2__main', mainClass]">
      <div :class="['public-ui-v2__content', contentClass]">
        <slot />
      </div>
    </div>
    <PublicFooter v-if="showChrome && showFooter" />
  </div>
  <slot v-else />
</template>

<script setup lang="ts">
import PublicFooter from './PublicFooter.vue'
import PublicHeader from './PublicHeader.vue'
import { usePublicUiVersion } from '@/composables/usePublicUiVersion'

withDefaults(defineProps<{
  showChrome?: boolean
  showFooter?: boolean
  mainClass?: string | string[] | Record<string, boolean>
  contentClass?: string | string[] | Record<string, boolean>
}>(), {
  showChrome: true,
  showFooter: true,
  mainClass: '',
  contentClass: '',
})

const { isPublicUiV2 } = usePublicUiVersion()
</script>

<style scoped>
.public-ui-v2 {
  --public-content-width: 80rem;
  --ui2-page: #f5f5f7;
  --ui2-surface: #ffffff;
  --ui2-surface-soft: rgba(255, 255, 255, 0.78);
  --ui2-surface-muted: #f7f7f8;
  --ui2-surface-hover: #f0f1f3;
  --ui2-toolbar: rgba(245, 245, 247, 0.74);
  --ui2-text: #1d1d1f;
  --ui2-text-secondary: #68686e;
  --ui2-text-tertiary: #8b8b92;
  --ui2-line: rgba(24, 24, 27, 0.1);
  --ui2-line-strong: rgba(24, 24, 27, 0.16);
  --ui2-accent: #087af5;
  --ui2-accent-soft: rgba(8, 122, 245, 0.1);
  --ui2-green: #128a4b;
  --ui2-green-soft: rgba(18, 138, 75, 0.1);
  --ui2-amber: #b25b00;
  --ui2-amber-soft: rgba(255, 149, 0, 0.12);
  --ui2-violet: #7251c8;
  --ui2-violet-soft: rgba(114, 81, 200, 0.1);
  --ui2-shadow: 0 1px 2px rgba(0, 0, 0, 0.035), 0 8px 24px rgba(0, 0, 0, 0.025);
  --public-ui-surface: var(--ui2-surface);
  --public-ui-surface-muted: var(--ui2-surface-muted);
  --public-ui-surface-hover: var(--ui2-surface-hover);
  --public-ui-text: var(--ui2-text);
  --public-ui-text-secondary: var(--ui2-text-secondary);
  --public-ui-text-tertiary: var(--ui2-text-tertiary);
  --public-ui-line: var(--ui2-line);
  --public-ui-accent: var(--ui2-accent);
  --public-ui-accent-soft: var(--ui2-accent-soft);
  --public-ui-green: var(--ui2-green);
  --public-ui-green-soft: var(--ui2-green-soft);
  --public-ui-shadow: var(--ui2-shadow);
  display: flex;
  min-height: 100dvh;
  flex-direction: column;
  overflow-x: clip;
  background: var(--ui2-page, #f5f6f8);
  color: var(--ui2-text, #111827);
  font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
  font-optical-sizing: auto;
  letter-spacing: 0;
}

:global(.dark .public-ui-v2) {
  --ui2-page: #111113;
  --ui2-surface: #1d1d20;
  --ui2-surface-soft: rgba(30, 30, 33, 0.84);
  --ui2-surface-muted: #242427;
  --ui2-surface-hover: #29292d;
  --ui2-toolbar: rgba(17, 17, 19, 0.76);
  --ui2-text: #f5f5f7;
  --ui2-text-secondary: #b1b1b7;
  --ui2-text-tertiary: #818188;
  --ui2-line: rgba(255, 255, 255, 0.09);
  --ui2-line-strong: rgba(255, 255, 255, 0.16);
  --ui2-accent: #4da3ff;
  --ui2-accent-soft: rgba(77, 163, 255, 0.13);
  --ui2-green: #48c982;
  --ui2-green-soft: rgba(72, 201, 130, 0.12);
  --ui2-amber: #ffad42;
  --ui2-amber-soft: rgba(255, 173, 66, 0.13);
  --ui2-violet: #aa8df0;
  --ui2-violet-soft: rgba(170, 141, 240, 0.13);
  --ui2-shadow: 0 1px 2px rgba(0, 0, 0, 0.18), 0 10px 28px rgba(0, 0, 0, 0.12);
}

.public-ui-v2__main { display: flex; width: 100%; flex: 1; flex-direction: column; }
.public-ui-v2__content { width: 100%; max-width: var(--public-content-width); margin: 0 auto; padding: 2rem 1.25rem 3rem; }
.public-ui-v2__content.public-ui-v2__content--flush { max-width: none; padding: 0; }

@media (max-width: 40rem) {
  .public-ui-v2__content { padding: 1.25rem 1rem 2rem; }
}

@media (prefers-reduced-motion: reduce) {
  .public-ui-v2 *,
  .public-ui-v2 *::before,
  .public-ui-v2 *::after { scroll-behavior: auto !important; animation-duration: 1ms !important; animation-iteration-count: 1 !important; }
}
</style>
