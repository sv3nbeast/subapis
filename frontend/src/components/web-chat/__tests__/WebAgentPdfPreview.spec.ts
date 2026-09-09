import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WebAgentPdfPreview from '../WebAgentPdfPreview.vue'
const mocks = vi.hoisted(() => ({ blob: vi.fn(), open: vi.fn() }))
vi.mock('@/api/webAgent', () => ({ getArtifactBlob: mocks.blob }))
vi.mock('@/utils/webAgentPdf', () => ({ openWebAgentPDF: mocks.open }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
beforeEach(() => {
  vi.clearAllMocks()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as CanvasRenderingContext2D)
  mocks.blob.mockResolvedValue(new Blob(['synthetic']))
})
function documentTask(text: string, pending?: Promise<void>) {
  const render = { cancel: vi.fn(), promise: pending || Promise.resolve() }
  const page = { getViewport: ({ scale }: { scale: number }) => ({ width: 800 * scale, height: 600 * scale }), render: vi.fn(() => render), getTextContent: vi.fn().mockResolvedValue({ items: [{ str: text }] }) }
  const pdf = { numPages: 2, getPage: vi.fn().mockResolvedValue(page) }
  return { loading: { promise: Promise.resolve(pdf), destroy: vi.fn().mockResolvedValue(undefined) }, render, pdf }
}
describe('private PDF preview lifecycle', () => {
  it('renders pages as canvas/text and destroys resources on close', async () => {
    const doc = documentTask('readable content'); mocks.open.mockResolvedValue(doc.loading)
    const wrapper = mount(WebAgentPdfPreview, { props: { artifactId: 1 } }); await flushPromises()
    expect(wrapper.find('canvas').exists()).toBe(true)
    expect(wrapper.text()).toContain('readable content')
    expect(wrapper.find('iframe').exists()).toBe(false)
    await wrapper.find('select').setValue('2'); await flushPromises()
    expect(doc.pdf.getPage).toHaveBeenLastCalledWith(2)
    wrapper.unmount(); expect(doc.loading.destroy).toHaveBeenCalled()
  })
  it('cannot replace a newly selected file with a late old page', async () => {
    let finish!: () => void
    const old = documentTask('old private content', new Promise(resolve => { finish = resolve }))
    const current = documentTask('current content')
    mocks.open.mockResolvedValueOnce(old.loading).mockResolvedValueOnce(current.loading)
    const wrapper = mount(WebAgentPdfPreview, { props: { artifactId: 1 } }); await flushPromises()
    await wrapper.setProps({ artifactId: 2 }); await flushPromises()
    expect(old.render.cancel).toHaveBeenCalled(); expect(old.loading.destroy).toHaveBeenCalled()
    finish(); await flushPromises()
    expect(wrapper.text()).toContain('current content'); expect(wrapper.text()).not.toContain('old private content')
    wrapper.unmount()
  })
  it('shows an error without embedding failed preview data', async () => {
    mocks.blob.mockRejectedValue(new Error('not authorized'))
    const wrapper = mount(WebAgentPdfPreview, { props: { artifactId: 1 } }); await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toBe('webAgent.previewFailed')
    expect(wrapper.find('canvas').exists()).toBe(false); expect(mocks.open).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
