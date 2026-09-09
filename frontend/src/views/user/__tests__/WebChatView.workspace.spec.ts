import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import WebChatView from '../WebChatView.vue'
import WebChatHome from '@/components/web-chat/WebChatHome.vue'

const mocks=vi.hoisted(()=>({
 query:{} as Record<string,string>,
 api:{getOptions:vi.fn(),listSessions:vi.fn(),getSession:vi.fn(),listProjects:vi.fn(),listTemplates:vi.fn(),listMessages:vi.fn(),createSession:vi.fn(),streamMessage:vi.fn()},
 agent:{listTasks:vi.fn(),listArtifacts:vi.fn(),getTaskEvents:vi.fn(),createTask:vi.fn(),cancelTask:vi.fn(),getArtifact:vi.fn(),getArtifactVersions:vi.fn(),getArtifactBlob:vi.fn(),deleteArtifact:vi.fn()},
 replace:vi.fn().mockResolvedValue(undefined),
}))
vi.mock('@/api/webChat',()=>({default:mocks.api}))
vi.mock('@/api/webAgent',()=>({...mocks.agent,isTaskTerminal:(status:string)=>['succeeded','failed','cancelled','interrupted'].includes(status)}))
vi.mock('@/stores/auth',()=>({useAuthStore:()=>({user:{id:3,username:'Local test'}})}))
vi.mock('vue-router',()=>({useRoute:()=>({query:mocks.query}),useRouter:()=>({replace:mocks.replace})}))
vi.mock('vue-i18n',()=>({useI18n:()=>({t:(key:string)=>key,te:()=>false,locale:ref('zh')})}))
vi.mock('@/composables/useWebChatDocuments',()=>({useWebChatDocuments:()=>({
 pendingDocuments:ref([]),failedAttachments:ref([]),attachmentState:ref(''),
 uploadTemporaryDocuments:vi.fn(),retryFailedAttachment:vi.fn(),removePendingDocument:vi.fn(),
 removeFailedAttachment:vi.fn(),clearPendingDocuments:vi.fn(),discardPendingDocuments:vi.fn().mockResolvedValue(undefined),
})}))
const session=(id:number)=>({id,user_id:3,group_id:1,model:'test-model',title:'Session '+id,system_prompt:'',max_output_tokens:8192,knowledge_enabled:false,created_at:'2026-09-09T00:00:00Z',updated_at:'2026-09-09T00:00:00Z'})
const message=(id:number,sessionID:number,content:string)=>({id,session_id:sessionID,role:'assistant',content,status:'completed',sources:[]})
function render(){return mount(WebChatView,{global:{stubs:{
 RouterLink:{template:'<a><slot /></a>'},
 WebChatProjectDialog:true,WebChatTemplateDialog:true,WebChatKnowledgeLibrary:true,
 WebAgentPdfPreview:true,
 BaseDialog:{props:['show'],template:'<div v-if="show"><slot /></div>'},
}}})}
beforeEach(()=>{
 vi.clearAllMocks()
 localStorage.clear()
 sessionStorage.clear()
 mocks.agent.listTasks.mockResolvedValue({items:[],next_before:0})
 mocks.agent.listArtifacts.mockResolvedValue({items:[],next_before:0})
 mocks.agent.getTaskEvents.mockResolvedValue({items:[],next_after:0})
 mocks.query={}
 mocks.api.getOptions.mockResolvedValue({enabled:true,groups:[{id:1,name:'test',platform:'openai',models:[{name:'test-model'}]}],projects_enabled:true,files_enabled:true,templates_enabled:true,history_enabled:true,task_status:'ready',tasks_enabled:true,file_limits:{}})
 mocks.api.listSessions.mockResolvedValue([session(1),session(2)])
 mocks.api.listProjects.mockResolvedValue([])
 mocks.api.listTemplates.mockResolvedValue([])
 mocks.api.listMessages.mockResolvedValue([])
 mocks.api.createSession.mockResolvedValue(session(3))
 mocks.api.getSession.mockImplementation((id:number)=>Promise.resolve(session(id)))
})
describe('MONO workspace real entry',()=>{
 it('opens a real home instead of auto-opening the last conversation',async()=>{
  const wrapper=render();await flushPromises()
  expect(wrapper.findComponent(WebChatHome).exists()).toBe(true)
  expect(mocks.api.listMessages).not.toHaveBeenCalled()
  expect(wrapper.text()).toContain('Session 1')
  wrapper.unmount()
 })
 it('hides the artifact library entry when Agent is not configured',async()=>{
  mocks.api.getOptions.mockResolvedValue({enabled:true,groups:[{id:1,name:'test',platform:'openai',models:[{name:'test-model'}]}],projects_enabled:false,files_enabled:false,templates_enabled:false,history_enabled:true,task_status:'not_configured',tasks_enabled:false,file_limits:{}})
  const wrapper=render();await flushPromises()
  expect(wrapper.findAll('.workbench-bar nav button').some(button=>button.text()==='workspace.files')).toBe(false)
  wrapper.unmount()
 })
 it('restores a conversation from its URL',async()=>{
  mocks.query={session:'1'}
  mocks.api.listMessages.mockResolvedValue([message(9,1,'persisted answer')])
  const wrapper=render();await flushPromises()
  expect(mocks.api.listMessages).toHaveBeenCalledWith(1)
  expect(wrapper.text()).toContain('persisted answer')
  expect(wrapper.findComponent(WebChatHome).exists()).toBe(false)
  wrapper.unmount()
 })
 it('does not replace a newly selected conversation with a late older fetch',async()=>{
  let resolveFirst:(value:unknown)=>void=()=>{}
  mocks.api.listMessages.mockImplementation((id:number)=>id===1?new Promise(r=>{resolveFirst=r}):Promise.resolve([message(12,2,'new answer')]))
  const wrapper=render();await flushPromises()
  await wrapper.findAll('.session-select')[0]!.trigger('click');await flushPromises()
  await wrapper.findAll('.session-select')[1]!.trigger('click');await flushPromises()
  resolveFirst([message(11,1,'stale answer')]);await flushPromises()
  expect(wrapper.text()).toContain('new answer')
  expect(wrapper.text()).not.toContain('stale answer')
  wrapper.unmount()
 })
 it('retains a draft and surfaces errors when submission was not accepted',async()=>{
  mocks.api.streamMessage.mockRejectedValue(new Error('wire failed'))
  const wrapper=render();await flushPromises()
  await wrapper.find('.composer-input').setValue('preserve my task')
  await wrapper.find('.composer').trigger('submit');await flushPromises()
  expect(mocks.api.streamMessage).toHaveBeenCalledTimes(1)
  expect((wrapper.find('.composer-input').element as HTMLTextAreaElement).value).toBe('preserve my task')
  expect(wrapper.find('[role="alert"]').text()).toContain('wire failed')
  wrapper.unmount()
 })
 it('submits an explicit file task instead of sending a text-only chat request',async()=>{
  const options=await mocks.api.getOptions();mocks.api.getOptions.mockResolvedValue({...options,tasks_enabled:true})
  mocks.agent.createTask.mockResolvedValue({id:20,session_id:3,kind:'document',prompt:'create my document',model:'test-model',status:'queued'})
  const wrapper=render();await flushPromises()
  await wrapper.findAll('.task-modebar button')[3]!.trigger('click')
  await wrapper.find('.composer-input').setValue('create my document')
  await wrapper.find('.composer').trigger('submit');await flushPromises()
  expect(mocks.api.streamMessage).not.toHaveBeenCalled()
  expect(mocks.agent.createTask).toHaveBeenCalledWith(3,expect.objectContaining({kind:'document',prompt:'create my document',group_id:1,model:'test-model'}))
  expect((wrapper.find('.composer-input').element as HTMLTextAreaElement).value).toBe('')
  expect(wrapper.text()).toContain('webAgent.queued')
  wrapper.unmount()
 })
 it('opens artifacts without reference storage and returns to their original conversation',async()=>{
  const options=await mocks.api.getOptions();mocks.api.getOptions.mockResolvedValue({...options,tasks_enabled:true,projects_enabled:false,files_enabled:false})
  const artifact={id:70,task_id:80,session_id:9,lineage_id:'one',version:1,kind:'document',title:'Saved report',filename:'report.docx',size_bytes:100,created_at:'2026-09-09T00:00:00Z'}
  mocks.agent.listArtifacts.mockResolvedValue({items:[artifact],next_before:0})
  const wrapper=render();await flushPromises()
  const files=wrapper.findAll('.workbench-bar nav button').find(b=>b.text()==='workspace.files')!
  await files.trigger('click');await flushPromises()
  expect(wrapper.find('.artifact-library').text()).toContain('Saved report')
  await wrapper.find('.file-session').trigger('click');await flushPromises()
  expect(mocks.api.getSession).toHaveBeenCalledWith(9)
  expect(mocks.api.listMessages).toHaveBeenLastCalledWith(9)
  expect(wrapper.find('.task-feed').exists()).toBe(true)
  expect(mocks.api.createSession).not.toHaveBeenCalled();wrapper.unmount()
 })
 it('restores an older session by owned lookup when it is outside the recent list',async()=>{
  mocks.query={session:'99'}
  const wrapper=render();await flushPromises()
  expect(mocks.api.getSession).toHaveBeenCalledWith(99)
  expect(mocks.api.listMessages).toHaveBeenLastCalledWith(99)
  wrapper.unmount()
 })
})
