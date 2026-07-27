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
  --ui2-surface-raised: #ffffff;
  --ui2-surface-muted: #f7f7f8;
  --ui2-surface-hover: #f0f1f3;
  --ui2-toolbar: rgba(245, 245, 247, 0.74);
  --ui2-text: #1d1d1f;
  --ui2-text-secondary: #68686e;
  --ui2-text-tertiary: #8b8b92;
  --ui2-line: rgba(24, 24, 27, 0.1);
  --ui2-line-strong: rgba(24, 24, 27, 0.16);
  --ui2-accent: #087af5;
  --ui2-accent-strong: #0f72d6;
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
  --ui2-page: #161618;
  --ui2-surface: #29292e;
  --ui2-surface-soft: rgba(35, 35, 38, 0.9);
  --ui2-surface-raised: #2d2d32;
  --ui2-surface-muted: #29292e;
  --ui2-surface-hover: #323237;
  --ui2-toolbar: rgba(24, 24, 27, 0.82);
  --ui2-text: #f4f4f6;
  --ui2-text-secondary: #c5c5ca;
  --ui2-text-tertiary: #94949c;
  --ui2-line: rgba(255, 255, 255, 0.1);
  --ui2-line-strong: rgba(255, 255, 255, 0.18);
  --ui2-accent: #409cff;
  --ui2-accent-strong: #0f72d6;
  --ui2-accent-soft: rgba(64, 156, 255, 0.15);
  --ui2-green: #5bd18b;
  --ui2-green-soft: rgba(91, 209, 139, 0.14);
  --ui2-amber: #f2b24c;
  --ui2-amber-soft: rgba(242, 178, 76, 0.14);
  --ui2-violet: #b7a0f5;
  --ui2-violet-soft: rgba(183, 160, 245, 0.15);
  --ui2-shadow: 0 1px 0 rgba(255, 255, 255, 0.025), 0 14px 34px rgba(0, 0, 0, 0.22);
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
