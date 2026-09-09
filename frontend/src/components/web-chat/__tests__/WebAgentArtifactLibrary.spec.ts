import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WebAgentArtifactLibrary from '../WebAgentArtifactLibrary.vue'
const api = vi.hoisted(() => ({ listArtifacts: vi.fn() }))
vi.mock('@/api/webAgent', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
const file = (id: number, lineage = `file-${id}`, version = 1, session_id = 9) => ({ id, task_id: id, session_id, lineage_id: lineage, version, kind: 'document', title: `Document ${id}`, filename: `file-${id}.docx`, size_bytes: 2048, created_at: '2026-09-09T00:00:00Z' })
const render = () => mount(WebAgentArtifactLibrary, { props: { enabled: true, userId: 1, sessions: [] } })
beforeEach(() => { vi.clearAllMocks(); api.listArtifacts.mockResolvedValue({ items: [], next_before: 0 }) })
describe('owned artifact library', () => {
  it('lists across sessions, groups versions, and routes file actions by identity', async () => {
    api.listArtifacts.mockResolvedValue({ items: [file(3, 'a', 2, 8), file(2, 'b', 1, 9), file(1, 'a', 1, 8)], next_before: 0 })
    const wrapper = render(); await flushPromises()
    expect(api.listArtifacts).toHaveBeenCalledWith({}, expect.any(AbortSignal))
    expect(wrapper.findAll('.library-file')).toHaveLength(2)
    await wrapper.find('.file-open').trigger('click'); expect(wrapper.emitted('open')).toEqual([[3]])
    await wrapper.find('.file-session').trigger('click'); expect(wrapper.emitted('conversation')?.[0]?.[0]).toMatchObject({ id: 3, session_id: 8 })
    wrapper.unmount()
  })
  it('keeps loaded results when loading the next page fails and supports retry', async () => {
    api.listArtifacts.mockResolvedValueOnce({ items: [file(50)], next_before: 50 }).mockRejectedValueOnce(new Error('page failed')).mockResolvedValueOnce({ items: [file(10)], next_before: 0 })
    const wrapper = render(); await flushPromises()
    await wrapper.find('.load-more').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Document 50'); expect(wrapper.find('[role="alert"]').text()).toContain('page failed')
    await wrapper.find('.load-more').trigger('click'); await flushPromises()
    expect(wrapper.findAll('.library-file')).toHaveLength(2); expect(wrapper.find('.load-more').exists()).toBe(false)
    wrapper.unmount()
  })
  it('does not show a late response from a previous signed-in user', async () => {
    let first!: (value: unknown) => void
    api.listArtifacts.mockReturnValueOnce(new Promise(resolve => { first = resolve })).mockResolvedValue({ items: [file(20)], next_before: 0 })
    const wrapper = render(); await flushPromises()
    await wrapper.setProps({ userId: 2 }); await flushPromises()
    first({ items: [file(10)], next_before: 0 }); await flushPromises()
    expect(wrapper.text()).toContain('Document 20'); expect(wrapper.text()).not.toContain('Document 10')
    wrapper.unmount()
  })
  it('does not resurrect a deleted artifact from a stale refresh', async () => {
    api.listArtifacts.mockResolvedValueOnce({ items: [file(10)], next_before: 0 })
    const wrapper = render(); await flushPromises()
    let refresh!: (value: unknown) => void
    api.listArtifacts.mockReturnValueOnce(new Promise(resolve => { refresh = resolve }))
    const pending = wrapper.vm.refresh(); wrapper.vm.remove(10)
    refresh({ items: [file(10)], next_before: 0 }); await pending
    expect(wrapper.findAll('.library-file')).toHaveLength(0); wrapper.unmount()
  })
})
