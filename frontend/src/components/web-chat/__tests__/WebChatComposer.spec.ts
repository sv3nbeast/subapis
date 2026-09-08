import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import WebChatComposer from '../WebChatComposer.vue'
vi.mock('vue-i18n', () => ({useI18n:()=>({t:(key:string)=>key})}))
const props={modelValue:'你好',disabled:false,canSend:true,sending:false,filesEnabled:true,templatesEnabled:true,templateName:'',documents:[],failedAttachments:[],attachmentState:''}
describe('MONO composer',()=>{
 it('does not submit an IME confirmation or Shift+Enter',async()=>{
  const wrapper=mount(WebChatComposer,{props})
  await wrapper.find('textarea').trigger('keydown',{key:'Enter',isComposing:true})
  await wrapper.find('textarea').trigger('keydown',{key:'Enter',shiftKey:true})
  expect(wrapper.emitted('submit')).toBeUndefined()
  await wrapper.find('textarea').trigger('keydown',{key:'Enter'})
  expect(wrapper.emitted('submit')).toHaveLength(1)
 })
 it.each([{disabled:true},{sending:true},{canSend:false},{modelValue:'  '}])('guards every submission path: %j',async overrides=>{
  const wrapper=mount(WebChatComposer,{props:{...props,...overrides}})
  await wrapper.find('form').trigger('submit')
  await wrapper.find('textarea').trigger('keydown',{key:'Enter'})
  expect(wrapper.emitted('submit')).toBeUndefined()
 })
 it('keeps stop available during generation and names icon-only buttons',async()=>{
  const wrapper=mount(WebChatComposer,{props:{...props,sending:true}})
  expect(wrapper.find('.btn-send').exists()).toBe(false)
  await wrapper.find('[aria-label="webChat.stop"]').trigger('click')
  expect(wrapper.emitted('stop')).toHaveLength(1)
  expect(wrapper.find('textarea').attributes('aria-label')).toBe('workspace.inputLabel')
 })
})
