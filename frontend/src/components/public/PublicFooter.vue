<template>
  <footer class="public-footer">
    <div class="public-footer__inner">
      <RouterLink class="public-footer__brand" to="/home">
        <img :src="siteLogo" :alt="t('common.logoAlt')" />
        <span>{{ siteName }}</span>
      </RouterLink>

      <nav class="public-footer__links" :aria-label="t('legalDocument.title')">
        <RouterLink v-for="item in legalLinks" :key="item.to" :to="item.to">
          {{ item.label }}
        </RouterLink>
      </nav>

      <p class="public-footer__copyright">
        &copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}
      </p>
    </div>
  </footer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { useAppStore } from '@/stores'
import { normalizeSiteName } from '@/utils/siteBrand'
import { sanitizeUrl } from '@/utils/url'

const { t } = useI18n()
const appStore = useAppStore()
const currentYear = new Date().getFullYear()
const siteName = computed(() => normalizeSiteName(appStore.siteName))
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '/logo.png', {
  allowRelative: true,
  allowDataUrl: true,
}) || '/logo.png')
const legalLinks = computed(() => [
  { to: '/legal/terms', label: t('home.footer.terms') },
  { to: '/legal/usage-policy', label: t('home.footer.usagePolicy') },
  { to: '/legal/supported-regions', label: t('home.footer.supportedRegions') },
  { to: '/legal/service-specific-terms', label: t('home.footer.serviceSpecificTerms') },
])
</script>

<style scoped>
.public-footer { border-top: 1px solid var(--ui2-line, rgba(15, 23, 42, 0.09)); color: var(--ui2-text-tertiary, #6b7280); }
.public-footer__inner {
  display: grid;
  max-width: 80rem;
  min-height: 5rem;
  margin: 0 auto;
  padding: 1rem 1.25rem;
  align-items: center;
  grid-template-columns: minmax(8rem, 1fr) auto minmax(8rem, 1fr);
  gap: 1.25rem;
}
.public-footer__brand { display: flex; min-width: 0; align-items: center; gap: 0.5rem; color: var(--ui2-text, #111827); font-size: 0.8125rem; font-weight: 700; text-decoration: none; }
.public-footer__brand img { width: 1.5rem; height: 1.5rem; border-radius: 6px; object-fit: contain; }
.public-footer__links { display: flex; align-items: center; justify-content: center; gap: 1rem; }
.public-footer__links a { color: inherit; font-size: 0.75rem; font-weight: 550; text-decoration: none; }
.public-footer__links a:hover { color: var(--ui2-text, #111827); }
.public-footer__links a:focus-visible,
.public-footer__brand:focus-visible { border-radius: 4px; outline: 2px solid var(--ui2-accent, #2563eb); outline-offset: 3px; }
.public-footer__copyright { margin: 0; font-size: 0.75rem; text-align: right; }

@media (max-width: 56rem) {
  .public-footer__inner { grid-template-columns: 1fr; justify-items: center; gap: 0.75rem; padding-block: 1.5rem; }
  .public-footer__links { flex-wrap: wrap; row-gap: 0.5rem; }
  .public-footer__copyright { text-align: center; }
}

@media (max-width: 40rem) {
  .public-footer__inner { padding-inline: 1rem; }
  .public-footer__links { gap: 0.5rem 0.875rem; }
}
</style>
