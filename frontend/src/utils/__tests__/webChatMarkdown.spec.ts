import { describe, expect, it } from 'vitest'
import { renderWebChatMarkdown } from '../webChatMarkdown'

describe('renderWebChatMarkdown', () => {
  it('renders tables and highlighted code', () => {
    const html = renderWebChatMarkdown('|A|B|\n|-|-|\n|1|2|\n\n```js\nconst ok = true\n```')
    expect(html).toContain('<table>')
    expect(html).toContain('web-chat-code-copy')
    expect(html).toContain('hljs-keyword')
  })

  it('removes scripts and unsafe attributes', () => {
    const html = renderWebChatMarkdown('<img src=x onerror="alert(1)"><script>alert(1)</script>')
    expect(html).not.toContain('onerror')
    expect(html).not.toContain('<script')
  })

  it('hardens outbound links', () => {
    const html = renderWebChatMarkdown('[OpenAI](https://openai.com)')
    expect(html).toContain('target="_blank"')
    expect(html).toContain('noopener')
  })
})
