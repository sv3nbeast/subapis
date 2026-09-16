import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import WebAgentTaskFeed from '../WebAgentTaskFeed.vue'
import type { WebAgentArtifact, WebAgentTask } from '@/api/webAgent'

const mocks = vi.hoisted(() => ({ blob: vi.fn() }))
vi.mock('@/api/webAgent', async () => {
  const actual = await vi.importActual<typeof import('@/api/webAgent')>('@/api/webAgent')
  return { ...actual, getArtifactBlob: mocks.blob }
})
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh' } }) }
})

beforeEach(() => {
  vi.clearAllMocks()
  mocks.blob.mockResolvedValue(new Blob(['png-bytes']))
  globalThis.URL.createObjectURL = vi.fn(() => 'blob:generated')
  globalThis.URL.revokeObjectURL = vi.fn()
})

function task(kind: string, artifactID: number): WebAgentTask {
  return {
    id: 16, session_id: 1, kind, prompt: '一只戴帽子的猫', status: 'succeeded',
    step_count: 2, created_at: '2026-09-16T07:32:10Z', updated_at: '2026-09-16T07:32:46Z',
    result: { artifact_id: artifactID },
  } as unknown as WebAgentTask
}
function artifact(kind: string, id: number): WebAgentArtifact {
  return {
    id, task_id: 16, session_id: 1, kind, title: '一只戴帽子的猫',
    filename: kind === 'image' ? 'cat.jpg' : 'deck.pptx', version: 1,
    mime: kind === 'image' ? 'image/jpeg' : 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
    size_bytes: 1024, created_at: '2026-09-16T07:32:46Z',
  } as unknown as WebAgentArtifact
}
function feed(kind: string) {
  return mount(WebAgentTaskFeed, {
    props: { tasks: [task(kind, 7)], events: {}, hasMore: false, loading: false, artifacts: [artifact(kind, 7)] },
    global: { stubs: { Icon: true } },
  })
}

describe('task feed artifact presentation', () => {
  // A generated image is the deliverable itself; requiring a pane click to see
  // it hid the result behind a file-shaped row.
  it('renders a generated image inline instead of only a file row', async () => {
    const wrapper = feed('image')
    await flushPromises()
    const image = wrapper.find('.wc-art-img img')
    expect(image.exists()).toBe(true)
    expect(image.attributes('src')).toBe('blob:generated')
    expect(mocks.blob).toHaveBeenCalledWith(7, false, expect.anything())
    await wrapper.find('.wc-art-img').trigger('click')
    expect(wrapper.emitted('open')?.[0]).toEqual([7])
    wrapper.unmount()
  })

  it('keeps office artifacts as a file row without fetching their bytes', async () => {
    const wrapper = feed('slides')
    await flushPromises()
    expect(wrapper.find('.wc-art-img').exists()).toBe(false)
    expect(wrapper.find('.wc-art').exists()).toBe(true)
    expect(mocks.blob).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
