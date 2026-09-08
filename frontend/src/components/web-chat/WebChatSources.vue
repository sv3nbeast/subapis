<template>
  <details v-if="sources.length" class="source-files">
    <summary>{{ t('workspace.sources', { files: grouped.length, citations: sources.length }) }}</summary>
    <div v-for="group in grouped" :key="group.id" class="source-file">
      <strong>{{ group.name }}</strong>
      <div><button v-for="source in group.sources" :key="source.index" @click="selected=source">{{ location(source) }}</button></div>
    </div>
    <BaseDialog :show="Boolean(selected)" :title="selected?.document_name||''" @close="selected=null">
      <p class="source-location">{{ selected ? location(selected) : '' }}</p>
      <pre class="excerpt">{{ selected?.excerpt }}</pre>
    </BaseDialog>
  </details>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { WebChatSource } from '@/api/webChat'
const props=defineProps<{ sources:WebChatSource[] }>()
const { t }=useI18n()
const selected=ref<WebChatSource|null>(null)
const grouped=computed(()=>{
 const groups=new Map<number,{id:number;name:string;sources:WebChatSource[]}>()
 for(const source of props.sources){
  if(!groups.has(source.document_id))groups.set(source.document_id,{id:source.document_id,name:source.document_name,sources:[]})
  groups.get(source.document_id)!.sources.push(source)
 }
 return [...groups.values()]
})
function location(s:WebChatSource){return s.page_number ? `${t('webChat.page')} ${s.page_number}` : s.location_label || `${t('webChat.source')} ${s.index}`}
</script>
<style scoped>
.source-files{font-size:.8125rem;color:var(--wa-muted,#71717a);margin-top:.75rem}.source-files summary{cursor:pointer}.source-file{border-left:2px solid var(--wa-line,#e4e4e7);padding:.5rem .75rem;margin-top:.5rem}.source-file strong{font-weight:550;overflow-wrap:anywhere}.source-file>div{display:flex;gap:.6rem;flex-wrap:wrap}.source-file button{color:var(--wa-accent,#2563eb);padding:.3rem 0;text-decoration:underline;text-underline-offset:3px}.source-location{font-size:.875rem;font-weight:600}.excerpt{white-space:pre-wrap;overflow-wrap:anywhere;max-height:60vh;overflow:auto;margin-top:.75rem;font: .875rem/1.6 system-ui,sans-serif}
</style>
