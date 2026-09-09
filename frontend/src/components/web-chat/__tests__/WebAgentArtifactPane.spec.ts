import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WebAgentArtifactPane from '../WebAgentArtifactPane.vue'
import type { WebAgentArtifact } from '@/api/webAgent'
const api = vi.hoisted(() => ({ getArtifactVersions: vi.fn(), getArtifactBlob: vi.fn(), deleteArtifact: vi.fn() }))
vi.mock('@/api/webAgent', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const artifact = (id: number) => ({ id, task_id: id, session_id: 1, lineage_id: 'same', version: id, kind: 'document', title: `Version ${id}`, filename: `version-${id}.docx`, mime: 'application/test', size_bytes: 100, sha256: 'hash', created_at: '2026-09-09T00:00:00Z' }) as WebAgentArtifact
function render(id = 1) { return mount(WebAgentArtifactPane, { props: { artifact: artifact(id), files: [artifact(id)], canRevise: true }, global: { stubs: { WebAgentPdfPreview: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' } } } }) }
beforeEach(() => { vi.clearAllMocks(); api.getArtifactVersions.mockResolvedValue({ items: [], next_before: 0 }) })
describe('artifact actions', () => {
  it('requires confirmation and only reports successful deletion', async () => {
    api.deleteArtifact.mockRejectedValueOnce(new Error('delete failed')).mockResolvedValue({ deleted: true, cleanup_pending: true })
    const wrapper = render(); await flushPromises()
    await wrapper.find('footer .delete').trigger('click')
    expect(api.deleteArtifact).not.toHaveBeenCalled()
    await wrapper.find('.confirm-delete').trigger('click'); await flushPromises()
    expect(wrapper.emitted('deleted')).toBeUndefined(); expect(wrapper.text()).toContain('delete failed')
    await wrapper.find('.confirm-delete').trigger('click'); await flushPromises()
    expect(api.deleteArtifact).toHaveBeenLastCalledWith(1); expect(wrapper.emitted('deleted')).toEqual([[1]])
    wrapper.unmount()
  })
  it('ignores stale version responses after switching the artifact', async () => {
    let first!: (value: unknown) => void
    api.getArtifactVersions.mockImplementation(id => id === 1 ? new Promise(resolve => { first = resolve }) : Promise.resolve({ items: [artifact(2)], next_before: 0 }))
    const wrapper = render(); await flushPromises()
    await wrapper.setProps({ artifact: artifact(2) }); await flushPromises()
    await wrapper.findAll('.artifact-tabs button')[2]!.trigger('click')
    first({ items: [artifact(1)], next_before: 0 }); await flushPromises()
    expect(wrapper.find('.file-list').text()).toContain('Version 2'); expect(wrapper.find('.file-list').text()).not.toContain('Version 1')
    wrapper.unmount()
  })
})
