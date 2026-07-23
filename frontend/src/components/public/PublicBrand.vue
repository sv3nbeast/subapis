<template>
  <RouterLink
    class="public-brand"
    :class="{ 'public-brand--collapse': collapseOnMobile }"
    to="/home"
  >
    <span class="public-brand__logo">
      <img :src="siteLogo || '/logo.png'" :alt="t('common.logoAlt')" />
    </span>
    <span class="public-brand__name">{{ siteName }}</span>
  </RouterLink>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

withDefaults(defineProps<{
  siteLogo: string
  siteName: string
  collapseOnMobile?: boolean
}>(), {
  collapseOnMobile: false,
})

const { t } = useI18n()
</script>

<style scoped>
.public-brand {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
  color: var(--ui2-text, #111827);
  text-decoration: none;
}

.public-brand__logo {
  width: 36px;
  height: 36px;
  flex: 0 0 36px;
  overflow: hidden;
  background: var(--ui2-surface, #fff);
  border: 1px solid var(--ui2-line, rgba(15, 23, 42, 0.09));
  border-radius: 10px;
  box-shadow: 0 5px 12px rgba(15, 23, 42, 0.12), 0 1px 2px rgba(15, 23, 42, 0.08);
}

.public-brand__logo img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.public-brand__name {
  overflow: hidden;
  color: inherit;
  font-size: 18px;
  font-style: italic;
  font-weight: 900;
  letter-spacing: 0;
  line-height: 1;
  text-overflow: ellipsis;
  transform: skewX(-6deg);
  white-space: nowrap;
}

.public-brand:focus-visible {
  border-radius: 8px;
  outline: 2px solid var(--ui2-accent, #087af5);
  outline-offset: 4px;
}

:global(.dark) .public-brand {
  color: #f5f5f7;
}

:global(.dark) .public-brand__logo {
  background: #1d1d20;
  border-color: rgba(255, 255, 255, 0.1);
  box-shadow: 0 5px 12px rgba(0, 0, 0, 0.22), 0 1px 2px rgba(0, 0, 0, 0.2);
}

@media (max-width: 640px) {
  .public-brand--collapse .public-brand__name {
    display: none;
  }
}
</style>
