import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import WebChatSources from '../WebChatSources.vue'
vi.mock('vue-i18n',()=>({useI18n:()=>({t:(key:string,p?:Record<string,number>)=>key==='workspace.sources'?`${p?.files} files / ${p?.citations} citations`:key})}))
describe('grouped source evidence',()=>{
 it('groups chunks by document without losing each citation',async()=>{
  const sources=[{index:1,document_id:4,document_name:'same.csv',excerpt:'row1',location_label:'row 1'},{index:2,document_id:4,document_name:'same.csv',excerpt:'row2',location_label:'row 2'},{index:3,document_id:5,document_name:'same.csv',excerpt:'different file'}]
  const wrapper=mount(WebChatSources,{props:{sources},global:{stubs:{BaseDialog:true}}})
  expect(wrapper.find('summary').text()).toBe('2 files / 3 citations')
  expect(wrapper.findAll('.source-file')).toHaveLength(2)
  expect(wrapper.findAll('.source-file button')).toHaveLength(3)
  expect(wrapper.find('details').attributes('open')).toBeUndefined()
  await wrapper.findAll('.source-file button')[1]!.trigger('click')
  expect(wrapper.findComponent({name:'BaseDialog'}).attributes('show')).toBe('true')
 })
 it('explains source provenance and clears stale evidence on context changes',async()=>{
  const source={index:1,document_id:4,document_name:'data.csv',excerpt:'safe excerpt',origin:'explicit' as const,included_chars:100,truncated:true,content_sha256:'original'}
  const wrapper=mount(WebChatSources,{props:{sources:[source]},global:{stubs:{BaseDialog:{props:['show'],template:'<section v-if="show" class="source-dialog"><slot/></section>'}}}})
  await wrapper.find('.source-file button').trigger('click')
  expect(wrapper.find('.source-dialog').text()).toContain('workspace.sourceOrigin.explicit')
  expect(wrapper.find('.source-dialog').text()).toContain('workspace.sourceTruncated')
  await wrapper.setProps({sources:[{...source}]})
  expect(wrapper.find('.source-dialog').exists()).toBe(true)
  await wrapper.setProps({sources:[{...source,excerpt:'different context',content_sha256:'changed'}]})
  expect(wrapper.find('.source-dialog').exists()).toBe(false)
  wrapper.unmount()
 })
})
