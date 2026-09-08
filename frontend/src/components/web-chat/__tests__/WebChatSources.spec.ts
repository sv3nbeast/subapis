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
})
