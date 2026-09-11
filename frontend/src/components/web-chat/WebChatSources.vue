<template>
  <details v-if="sources.length" class="source-files wc-sources">
    <summary class="wc-src-summary"><Icon name="book" size="xs" />{{ t('workspace.sources', { files: grouped.length, citations: sources.length }) }}</summary>
    <div v-for="group in grouped" :key="group.id" class="source-file wc-src-group">
      <strong>{{ group.name }}</strong>
      <div><button v-for="source in group.sources" :key="source.index" @click="selected=source">{{ location(source) }}</button></div>
    </div>
    <BaseDialog :show="Boolean(selected)" :title="selected?.document_name||''" @close="selected=null">
      <p class="source-location wc-sl-sec">{{ selected ? location(selected) : '' }}</p>
      <p v-if="selected?.origin">{{ t(`workspace.sourceOrigin.${selected.origin}`) }}</p>
      <p v-if="selected?.included_chars">{{ t('workspace.sourceIncluded',{count:selected.included_chars}) }} <span v-if="selected.truncated">{{ t('workspace.sourceTruncated') }}</span></p>
      <pre class="excerpt wc-excerpt">{{ selected?.excerpt }}</pre>
    </BaseDialog>
  </details>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import type { WebChatSource } from '@/api/webChat'
const props=defineProps<{ sources:WebChatSource[] }>()
const { t }=useI18n()
const selected=ref<WebChatSource|null>(null)
watch(()=>props.sources,sources=>{if(selected.value){const old=selected.value;selected.value=sources.find(s=>s.document_id===old.document_id&&s.index===old.index&&s.content_sha256===old.content_sha256&&s.excerpt===old.excerpt)||null}})
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
.wc-sources{font-size:11.5px;color:var(--wc-ink3);margin-top:10px}
.wc-sources summary{cursor:pointer}
</style>
