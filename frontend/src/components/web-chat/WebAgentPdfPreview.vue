<template>
  <section class="pdf-preview" :aria-label="t('webAgent.preview')">
    <div class="page-toolbar">
      <button :disabled="page <= 1 || rendering" :aria-label="t('common.previous')" @click="page--">‹</button>
      <label>{{ t('webAgent.pageLabel') }} <select v-model.number="page" :disabled="rendering || !pages" :aria-label="t('webAgent.pageLabel')"><option v-for="n in pages" :key="n" :value="n">{{ n }}</option></select> / {{ pages || '—' }}</label>
      <button :disabled="page >= pages || rendering" :aria-label="t('common.next')" @click="page++">›</button>
    </div>
    <p v-if="loading || rendering" role="status" class="preview-notice">{{ t('common.loading') }}</p>
    <p v-if="error" role="alert" class="preview-notice">{{ error }}</p>
    <div ref="surface" class="page-surface" role="img" :aria-label="t('webAgent.pageDescription', { page, pages })" />
    <div class="sr-only">{{ pageText }}</div>
  </section>
</template>
<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PDFDocumentLoadingTask, PDFDocumentProxy, RenderTask } from 'pdfjs-dist'
import { getArtifactBlob } from '@/api/webAgent'
import { openWebAgentPDF } from '@/utils/webAgentPdf'
const props = defineProps<{ artifactId: number }>()
const { t } = useI18n()
const surface = ref<HTMLElement>(), page = ref(1), pages = ref(0), pageText = ref(''), loading = ref(false), rendering = ref(false), error = ref('')
let epoch = 0, drawing = 0, controller: AbortController | undefined, documentTask: PDFDocumentLoadingTask | undefined, pdf: PDFDocumentProxy | undefined, renderTask: RenderTask | undefined
function release() {
  epoch++; drawing++; controller?.abort(); renderTask?.cancel(); renderTask = undefined
  if (documentTask) void documentTask.destroy().catch(() => {})
  documentTask = undefined; pdf = undefined; surface.value?.replaceChildren(); pageText.value = ''
}
async function draw() {
  const version = epoch, drawID = ++drawing, current = pdf
  if (!current || !surface.value || page.value < 1 || page.value > current.numPages) return
  renderTask?.cancel(); rendering.value = true; error.value = ''; pageText.value = ''
  try {
    const target = await current.getPage(page.value)
    if (version !== epoch || drawID !== drawing) return
    const natural = target.getViewport({ scale: 1 })
    if (!(natural.width > 0 && natural.height > 0 && Number.isFinite(natural.width) && Number.isFinite(natural.height))) throw new Error('Invalid page size')
    const width = Math.min(surface.value.clientWidth || 800, 1100)
    const scale = Math.min(width / natural.width * Math.min(window.devicePixelRatio || 1, 2), 2200 / Math.max(natural.width, natural.height))
    const viewport = target.getViewport({ scale })
    const canvas = document.createElement('canvas')
    canvas.width = Math.ceil(viewport.width); canvas.height = Math.ceil(viewport.height)
    canvas.style.width = '100%'; canvas.style.height = 'auto'
    const context = canvas.getContext('2d')
    if (!context) throw new Error('Canvas is unavailable')
    const render = target.render({ canvas, canvasContext: context, viewport })
    renderTask = render
    await render.promise
    if (version !== epoch || drawID !== drawing) return
    surface.value?.replaceChildren(canvas)
    const text = await target.getTextContent()
    if (version === epoch && drawID === drawing) pageText.value = text.items.map(item => 'str' in item ? item.str : '').join(' ')
  } catch { if (version === epoch && drawID === drawing) error.value = t('webAgent.previewFailed') }
  finally { if (version === epoch && drawID === drawing) rendering.value = false }
}
watch(() => props.artifactId, async id => {
  release(); const version = epoch
  loading.value = true; rendering.value = false; error.value = ''; pages.value = 0; page.value = 1
  controller = new AbortController()
  try {
    const blob = await getArtifactBlob(id, true, controller.signal)
    if (version !== epoch) return
    const task = await openWebAgentPDF(blob)
    if (version !== epoch) { await task.destroy(); return }
    documentTask = task
    const loaded = await task.promise
    if (version !== epoch) return
    pdf = loaded; pages.value = loaded.numPages
    await draw()
  } catch { if (version === epoch) error.value = t('webAgent.previewFailed') }
  finally { if (version === epoch) loading.value = false }
}, { immediate: true, flush: 'post' })
watch(page, () => { void draw() })
onBeforeUnmount(release)
</script>
<style scoped>
.pdf-preview{display:flex;flex-direction:column;gap:1rem;min-width:0}.page-toolbar{display:flex;align-items:center;justify-content:center;gap:.75rem;font-size:.875rem}.page-toolbar button{width:2rem;min-height:2rem;border:1px solid var(--wa-line);border-radius:5px}.page-toolbar select{background:var(--wa-bg);border:1px solid var(--wa-line);padding:.2rem .35rem;border-radius:4px}.page-surface{background:white;box-shadow:0 1px 8px #0001;min-width:0}.preview-notice{font-size:.875rem;color:var(--wa-muted);text-align:center}.page-toolbar button:disabled{opacity:.4}@media(max-width:767px){.page-toolbar button,.page-toolbar select{min-height:44px;min-width:44px}}
</style>
