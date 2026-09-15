<template>
  <figure class="image-preview" :aria-label="t('webAgent.preview')">
    <img v-if="source" :src="source" :alt="alt" />
    <p v-else-if="loading" role="status" class="preview-notice">{{ t('common.loading') }}</p>
    <p v-else class="preview-notice" role="alert">{{ error || t('webAgent.previewFailed') }}</p>
  </figure>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getArtifactBlob } from '@/api/webAgent'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ artifactId: number; alt: string }>()
const { t } = useI18n()
const source = ref(''), loading = ref(false), error = ref('')
// An image carries no separate preview blob, so it renders its own bytes.
let epoch = 0, controller = new AbortController()

function release() {
  if (source.value) URL.revokeObjectURL(source.value)
  source.value = ''
}

async function load(id: number) {
  const version = ++epoch
  controller.abort()
  controller = new AbortController()
  release()
  loading.value = true
  error.value = ''
  try {
    const blob = await getArtifactBlob(id, false, controller.signal)
    if (version !== epoch) return
    source.value = URL.createObjectURL(blob)
  } catch (e) {
    if (version === epoch && !controller.signal.aborted) error.value = extractApiErrorMessage(e)
  } finally {
    if (version === epoch) loading.value = false
  }
}

watch(() => props.artifactId, id => { if (id > 0) void load(id) }, { immediate: true })
onBeforeUnmount(() => { epoch++; controller.abort(); release() })
</script>

<style scoped>
.image-preview { display: flex; align-items: center; justify-content: center; margin: 0; min-width: 0; }
.image-preview img { max-width: 100%; height: auto; border-radius: 8px; background: white; box-shadow: 0 1px 8px #0001; }
.preview-notice { font-size: .875rem; color: var(--wa-muted); text-align: center; }
</style>
