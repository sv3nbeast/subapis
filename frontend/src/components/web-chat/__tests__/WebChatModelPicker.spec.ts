import { mount } from '@vue/test-utils'
import { describe,it,expect,vi } from 'vitest'
import { ref } from 'vue'
import WebChatModelPicker from '../WebChatModelPicker.vue'
vi.mock('vue-i18n',()=>({useI18n:()=>({locale:ref('zh-CN')})}))
const models=[{id:'opus-route',name:'Claude Opus 5',brand:'Claude',description:'写作',model:'claude-opus-5',billing_type:'subscription',recommended:true},{id:'astra-route',name:'GPT 6 Astra',brand:'OpenAI',description:'编程',model:'gpt-6-astra',billing_type:'standard',recommended:false}]
describe('WebChat model picker',()=>{
  it('filters models and emits only a catalog selection',async()=>{
    const wrapper=mount(WebChatModelPicker,{props:{modelValue:'opus-route',models}})
    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).toContain('使用订阅额度');expect(wrapper.text()).toContain('使用余额')
    await wrapper.get('input').setValue('Astra')
    const choice=wrapper.get('[aria-pressed]');expect(choice.text()).toContain('GPT 6 Astra')
    await choice.trigger('click');expect(wrapper.emitted('update:modelValue')).toEqual([['astra-route']]);expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('does not silently replace an unavailable saved model',async()=>{
    const wrapper=mount(WebChatModelPicker,{props:{modelValue:'removed',models}})
    expect(wrapper.get('button').text()).toContain('选择模型');expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('button').trigger('click');await wrapper.trigger('keydown',{key:'Escape'});expect(wrapper.find('[role="dialog"]').exists()).toBe(false);wrapper.unmount()
  })
  it('disables model changes while generating',()=>{
    const wrapper=mount(WebChatModelPicker,{props:{modelValue:'opus-route',models,disabled:true}})
    expect(wrapper.get('button').attributes('disabled')).toBeDefined();wrapper.unmount()
  })
})
