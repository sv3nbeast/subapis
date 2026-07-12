<template>
  <div
    v-if="markdown"
    class="web-chat-markdown"
    v-html="rendered"
    @click="handleClick"
  />
  <p v-else class="whitespace-pre-wrap break-words text-sm leading-6">{{ content }}</p>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { renderWebChatMarkdown } from '@/utils/webChatMarkdown'

const props = defineProps<{ content: string; markdown?: boolean }>()
const rendered = computed(() => renderWebChatMarkdown(props.content))

async function handleClick(event: MouseEvent) {
  const target = (event.target as HTMLElement).closest<HTMLButtonElement>('.web-chat-code-copy')
  if (!target) return
  const code = decodeURIComponent(target.dataset.code || '')
  await navigator.clipboard.writeText(code)
  const old = target.textContent
  target.textContent = 'Copied'
  window.setTimeout(() => { target.textContent = old }, 1200)
}
</script>

<style>
.web-chat-markdown { font-size: .875rem; line-height: 1.65; overflow-wrap: anywhere; }
.web-chat-markdown > * + * { margin-top: .75rem; }
.web-chat-markdown h1,.web-chat-markdown h2,.web-chat-markdown h3 { font-weight: 800; line-height: 1.3; }
.web-chat-markdown h1 { font-size: 1.35rem; }.web-chat-markdown h2 { font-size: 1.15rem; }.web-chat-markdown h3 { font-size: 1rem; }
.web-chat-markdown ul { list-style: disc; padding-left: 1.3rem; }.web-chat-markdown ol { list-style: decimal; padding-left: 1.3rem; }
.web-chat-markdown blockquote { border-left: 3px solid #14b8a6; padding-left: .8rem; color: #64748b; }
.web-chat-markdown table { width: 100%; border-collapse: collapse; display: block; overflow-x: auto; }
.web-chat-markdown th,.web-chat-markdown td { border: 1px solid rgba(148,163,184,.35); padding: .4rem .55rem; text-align: left; }
.web-chat-markdown a { color: #0d9488; text-decoration: underline; }
.web-chat-markdown :not(pre)>code { border-radius: .35rem; background: rgba(148,163,184,.18); padding: .1rem .3rem; }
.web-chat-code { overflow: hidden; border-radius: .8rem; background: #0f172a; color: #e2e8f0; }
.web-chat-code-head { display:flex; align-items:center; justify-content:space-between; padding:.35rem .65rem; background:#1e293b; color:#94a3b8; font-size:.7rem; text-transform:uppercase; }
.web-chat-code-copy { color:#cbd5e1; text-transform:none; }.web-chat-code-copy:hover { color:white; }
.web-chat-code pre { overflow-x:auto; padding:.75rem; margin:0; }.web-chat-code code { background:transparent; padding:0; }
.hljs-keyword,.hljs-selector-tag,.hljs-literal { color:#c084fc }.hljs-string,.hljs-attr { color:#86efac }.hljs-number { color:#fbbf24 }.hljs-comment { color:#64748b }.hljs-title,.hljs-function { color:#67e8f9 }
</style>
