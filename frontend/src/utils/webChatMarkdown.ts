import DOMPurify from 'dompurify'
import { Marked } from 'marked'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import css from 'highlight.js/lib/languages/css'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import python from 'highlight.js/lib/languages/python'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('shell', bash)
hljs.registerLanguage('css', css)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('md', markdown)
hljs.registerLanguage('python', python)
hljs.registerLanguage('py', python)
hljs.registerLanguage('sql', sql)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('ts', typescript)

const marked = new Marked({
  gfm: true,
  breaks: true,
  renderer: {
    code({ text, lang }) {
      const language = (lang || '').trim().split(/\s+/)[0].toLowerCase()
      const highlighted = language && hljs.getLanguage(language)
        ? hljs.highlight(text, { language }).value
        : escapeHTML(text)
      const label = language || 'text'
      return `<div class="web-chat-code"><div class="web-chat-code-head"><span>${escapeHTML(label)}</span><button type="button" class="web-chat-code-copy" data-code="${encodeURIComponent(text)}">Copy</button></div><pre><code class="hljs language-${escapeHTML(label)}">${highlighted}</code></pre></div>`
    },
  },
})

export function renderWebChatMarkdown(source: string): string {
  const parsed = marked.parse(source || '') as string
  const sanitized = DOMPurify.sanitize(parsed, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['style', 'script', 'iframe', 'object', 'embed', 'form'],
    FORBID_ATTR: ['style', 'onerror', 'onclick', 'onload'],
    ADD_ATTR: ['target', 'rel', 'data-code'],
  })
  if (typeof document === 'undefined') return sanitized
  const root = document.createElement('div')
  root.innerHTML = sanitized
  root.querySelectorAll('a').forEach((link) => {
    link.setAttribute('target', '_blank')
    link.setAttribute('rel', 'noopener noreferrer nofollow')
  })
  return root.innerHTML
}

function escapeHTML(value: string): string {
  return value.replace(/[&<>'"]/g, (character) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;',
  })[character] || character)
}
