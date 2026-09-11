import { mount,flushPromises } from '@vue/test-utils'
import { beforeEach,describe,it,expect,vi } from 'vitest'
import { ref } from 'vue'
import WebChatModelCatalogEditor from '../WebChatModelCatalogEditor.vue'
const mocks=vi.hoisted(()=>({get:vi.fn(),save:vi.fn(),validate:vi.fn(),groups:vi.fn()}))
vi.mock('@/api/webChat',()=>({getModelCatalog:mocks.get,saveModelCatalog:mocks.save,validateModelCatalog:mocks.validate}))
vi.mock('@/api/admin/groups',()=>({getAll:mocks.groups}))
vi.mock('vue-i18n',()=>({useI18n:()=>({locale:ref('zh-CN')})}))
beforeEach(()=>{
  vi.clearAllMocks();vi.spyOn(window,'confirm').mockReturnValue(true)
  mocks.get.mockResolvedValue({entries:[{id:'opus',name:'Claude Opus 5',brand:'Claude',description:'',group_id:2,model:'claude-opus-5',enabled:true,recommended:true,sort_order:0}]})
  mocks.groups.mockResolvedValue([{id:2,name:'Browser',platform:'anthropic',subscription_type:'standard'},{id:3,name:'CLI only',claude_code_only:true}])
  mocks.validate.mockResolvedValue({valid:true});mocks.save.mockImplementation(async c=>c)
})
describe('WebChat admin catalog',()=>{
  it('publishes explicitly and excludes CLI-only groups',async()=>{
    const wrapper=mount(WebChatModelCatalogEditor);await flushPromises()
    expect(wrapper.text()).not.toContain('CLI only');expect(mocks.save).not.toHaveBeenCalled()
    await wrapper.get('input[maxlength="100"]').setValue('Opus Latest')
    const button=wrapper.findAll('button').find(b=>b.text()==='发布模型目录')!
    await button.trigger('click');await flushPromises()
    expect(mocks.save).toHaveBeenCalledWith({entries:[expect.objectContaining({name:'Opus Latest',group_id:2,model:'claude-opus-5'})]})
    expect(wrapper.text()).toContain('模型目录已发布');wrapper.unmount()
  })
  it('validation errors are visible and do not publish',async()=>{
    mocks.validate.mockRejectedValue(new Error('invalid route'))
    const wrapper=mount(WebChatModelCatalogEditor);await flushPromises()
    await wrapper.findAll('button').find(b=>b.text().startsWith('校验配置'))!.trigger('click');await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalid route');expect(mocks.save).not.toHaveBeenCalled();wrapper.unmount()
  })
})
