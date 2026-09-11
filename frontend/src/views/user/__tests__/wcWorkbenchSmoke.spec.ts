import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import WebChatView from '@/views/user/WebChatView.vue'

const mocks = vi.hoisted(() => ({
  api: { getOptions: vi.fn(), listSessions: vi.fn(), getSession: vi.fn(), listProjects: vi.fn(), listTemplates: vi.fn(), listMessages: vi.fn(), createSession: vi.fn(), streamMessage: vi.fn() },
  agent: { listTasks: vi.fn(), listArtifacts: vi.fn(), getTaskEvents: vi.fn(), createTask: vi.fn(), cancelTask: vi.fn(), getArtifact: vi.fn(), getArtifactVersions: vi.fn(), getArtifactBlob: vi.fn(), deleteArtifact: vi.fn() },
  replace: vi.fn().mockResolvedValue(undefined),
  errors: [] as string[],
}))
vi.mock('@/api/webChat', () => ({ default: mocks.api }))
vi.mock('@/api/webAgent', () => ({ ...mocks.agent, isTaskTerminal: (s: string) => ['succeeded','failed','cancelled','interrupted'].includes(s) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 7, username: 'sven' } }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: mocks.replace }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (k: string) => k, te: () => false, locale: ref('zh') }) }))

const now = new Date().toISOString()
// 真实形状：老会话没有 group_id / platform 等字段
const sparseSessions = [
  { id: 1, user_id: 7, group_id: 1, group_name: '默认', platform: 'anthropic', model: 'claude-opus-5', title: '正常会话', system_prompt: '', temperature: null, max_output_tokens: 8192, project_id: null, project_name: '', default_template_id: null, active_leaf_message_id: null, knowledge_enabled: false, pinned_at: null, created_at: now, updated_at: now },
  { id: 2, user_id: 7, group_id: 999, model: 'removed-model', title: '', system_prompt: '', temperature: null, max_output_tokens: 8192, project_id: 5, knowledge_enabled: false, pinned_at: now, created_at: now, updated_at: now },
  { id: 3, user_id: 7, group_id: 1, group_name: '默认', platform: 'anthropic', model: 'claude-opus-5', title: '无项目', system_prompt: '', temperature: null, max_output_tokens: 8192, project_id: null, knowledge_enabled: false, pinned_at: null, created_at: now, updated_at: now },
] as any[]

function render() {
  return mount(WebChatView, {
    global: {
      stubs: { RouterLink: { template: '<a><slot /></a>' }, WebChatProjectDialog: true, WebChatTemplateDialog: true, WebChatKnowledgeLibrary: true, WebAgentPdfPreview: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } },
      config: { errorHandler: (err: unknown) => { mocks.errors.push(String((err as Error)?.stack || err)) } },
    },
  })
}

beforeEach(() => {
  vi.clearAllMocks(); mocks.errors = []; localStorage.clear(); sessionStorage.clear()
  mocks.agent.listTasks.mockResolvedValue({ items: [], next_before: 0 })
  mocks.agent.listArtifacts.mockResolvedValue({ items: [], next_before: 0 })
  mocks.agent.getTaskEvents.mockResolvedValue({ items: [], next_after: 0 })
  mocks.api.getOptions.mockResolvedValue({ enabled: true, groups: [{ id: 1, name: '默认', platform: 'anthropic', subscription_type: 'subscription', rate_multiplier: 1, models: [{ name: 'claude-opus-5', display_name: 'Claude Opus 5' }] }], default_group_id: 1, default_model: 'claude-opus-5', projects_enabled: true, templates_enabled: true, history_enabled: true, files_enabled: true, task_status: 'ready', tasks_enabled: true, file_limits: {} })
  mocks.api.listSessions.mockResolvedValue(sparseSessions)
  mocks.api.listProjects.mockResolvedValue([{ id: 5, user_id: 7, name: '项目', description: '', color: '#0f9f8f', sort_order: 1, session_count: 1, created_at: now, updated_at: now }])
  mocks.api.listTemplates.mockResolvedValue([])
  mocks.api.listMessages.mockResolvedValue([])
})

describe('workbench renders with real-shaped data', () => {
  it('survives null collections from the API (Go marshals empty slices as null)', async () => {
    mocks.api.getOptions.mockResolvedValue({ enabled: true, groups: null, projects_enabled: true, templates_enabled: true, history_enabled: true, files_enabled: true, task_status: 'not_configured', tasks_enabled: false, file_limits: {} })
    mocks.api.listSessions.mockResolvedValue(null)
    mocks.api.listProjects.mockResolvedValue(null)
    mocks.api.listTemplates.mockResolvedValue(null)
    const wrapper = render(); await flushPromises()
    expect(mocks.errors).toEqual([])
    expect(wrapper.find('.web-agent-workbench').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps rendering when a session references a deleted project and messages are null', async () => {
    mocks.api.listProjects.mockResolvedValue(null)
    mocks.api.listMessages.mockResolvedValue(null)
    const wrapper = render(); await flushPromises()
    expect(mocks.errors).toEqual([])
    expect(wrapper.text()).toContain('正常会话')
    wrapper.unmount()
  })

  it('renders without throwing and shows grouped sessions', async () => {
    const wrapper = render(); await flushPromises()
    expect(mocks.errors).toEqual([])
    expect(wrapper.text()).toContain('正常会话')
    expect(wrapper.text()).toContain('无项目')
    wrapper.unmount()
  })
})
