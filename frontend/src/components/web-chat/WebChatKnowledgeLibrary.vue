<template>
  <BaseDialog :show="show" :title="`${project?.name || ''} · ${t('webChat.knowledgeLibrary')}`" width="wide" @close="emit('close')">
    <div class="space-y-4" @dragover.prevent @drop.prevent="dropFiles">
      <label class="drop-zone">
        <input class="hidden" type="file" multiple accept=".pdf,.docx,.txt,.md,.csv" @change="pickFiles" />
        <Icon name="upload" size="lg" /><strong>{{ t('webChat.dropDocuments') }}</strong>
        <span>PDF · DOCX · TXT · Markdown · CSV · {{ maxSizeLabel }}</span>
      </label>
      <div v-for="upload in uploads" :key="upload.key" class="upload-row"><span class="truncate">{{ upload.name }}</span><progress :value="upload.progress" max="100"/><small>{{ upload.error || `${upload.progress}%` }}</small><button v-if="upload.error" class="btn btn-secondary btn-sm" @click="retryUpload(upload)">{{ t('webChat.retry') }}</button></div>
      <div class="flex gap-2"><button v-for="item in filters" :key="item.value" class="filter" :class="{active:filter===item.value}" @click="filter=item.value">{{ item.label }}</button></div>
      <div class="document-list">
        <article v-for="doc in visible" :key="doc.id" class="document-row">
          <div class="file-icon">{{ doc.extension.slice(1).toUpperCase() }}</div>
          <div class="min-w-0 flex-1"><strong class="block truncate text-sm">{{ doc.original_name }}</strong><p class="text-xs text-gray-500">{{ size(doc.size_bytes) }} · {{ statusLabel(doc) }}<span v-if="doc.error_message" class="text-red-500"> · {{ doc.error_message }}</span></p></div>
          <label v-if="doc.status==='ready'" class="text-xs"><input type="checkbox" :checked="doc.enabled" @change="toggle(doc)"/> {{ t('common.enabled') }}</label>
          <button v-if="doc.status==='failed'" class="btn btn-secondary btn-sm" @click="retry(doc)">{{ t('webChat.retry') }}</button>
          <button class="btn btn-secondary btn-sm" :disabled="doc.status!=='ready'" @click="webChatAPI.downloadDocument(doc.id,doc.original_name)">{{ t('common.download') }}</button>
          <button class="btn btn-secondary btn-sm text-red-600" @click="remove(doc)">{{ t('common.delete') }}</button>
        </article>
        <p v-if="!visible.length" class="py-8 text-center text-sm text-gray-500">{{ t('webChat.noDocuments') }}</p>
      </div>
    </div>
  </BaseDialog>
</template>
<script setup lang="ts">
import {computed,ref,watch,onBeforeUnmount}from'vue';import{useI18n}from'vue-i18n';import BaseDialog from '@/components/common/BaseDialog.vue';import Icon from '@/components/icons/Icon.vue';import webChatAPI,{type WebChatDocument,type WebChatProject,type WebChatDocumentLimits}from'@/api/webChat';import{extractApiErrorMessage}from'@/utils/apiError'
interface ProjectUpload {key:string;name:string;file:File;progress:number;error?:string}
const props=defineProps<{show:boolean;project:WebChatProject|null;limits:WebChatDocumentLimits}>();const emit=defineEmits<{close:[]}>();const{t}=useI18n();const documents=ref<WebChatDocument[]>([]),filter=ref('all'),uploads=ref<ProjectUpload[]>([]);let timer:ReturnType<typeof setInterval>|null=null
const filters=computed(()=>[{value:'all',label:t('common.all')},{value:'ready',label:t('webChat.documentReady')},{value:'processing',label:t('webChat.documentProcessing')},{value:'failed',label:t('webChat.documentFailed')}]);const visible=computed(()=>filter.value==='all'?documents.value:documents.value.filter(d=>filter.value==='processing'?['uploaded','processing'].includes(d.status):d.status===filter.value));const maxSizeLabel=computed(()=>size(props.limits.max_file_bytes||20*1024*1024))
watch(()=>[props.show,props.project?.id]as const,()=>{stopPoll();if(props.show&&props.project){void load();timer=setInterval(load,2500)}},{immediate:true});onBeforeUnmount(stopPoll);function stopPoll(){if(timer)clearInterval(timer);timer=null}async function load(){if(props.project)documents.value=await webChatAPI.listProjectDocuments(props.project.id).catch(()=>documents.value)}
function pickFiles(e:Event){void upload(Array.from((e.target as HTMLInputElement).files||[]));(e.target as HTMLInputElement).value=''}function dropFiles(e:DragEvent){void upload(Array.from(e.dataTransfer?.files||[]))}async function upload(files:File[]){if(!props.project)return;for(const file of files){const state:ProjectUpload={key:`${Date.now()}-${Math.random()}`,name:file.name,file,progress:0,error:''};uploads.value.push(state);await uploadOne(state)}}async function uploadOne(state:ProjectUpload){if(!props.project)return;state.error='';state.progress=0;try{await webChatAPI.uploadProjectDocument(props.project.id,state.file,p=>state.progress=p);state.progress=100;await load();setTimeout(()=>uploads.value=uploads.value.filter(item=>item.key!==state.key),1200)}catch(e){state.error=extractApiErrorMessage(e)}}async function retryUpload(state:ProjectUpload){await uploadOne(state)}
async function toggle(doc:WebChatDocument){Object.assign(doc,await webChatAPI.patchDocument(doc.id,!doc.enabled))}async function remove(doc:WebChatDocument){if(!confirm(t('webChat.deleteDocumentConfirm')))return;await webChatAPI.deleteDocument(doc.id);await load()}function size(v:number){if(v>=1024**2)return`${(v/1024**2).toFixed(1)} MB`;return`${Math.ceil(v/1024)} KB`}function statusLabel(d:WebChatDocument){return t(`webChat.documentStatus.${d.status}`)}
async function retry(doc:WebChatDocument){Object.assign(doc,await webChatAPI.retryDocument(doc.id))}
</script>
<style scoped>.drop-zone{display:flex;min-height:8rem;cursor:pointer;align-items:center;justify-content:center;flex-direction:column;gap:.35rem;border:2px dashed rgba(20,184,166,.35);border-radius:1rem;background:rgba(20,184,166,.05);color:#0f766e}.drop-zone span{font-size:.72rem;color:#64748b}.upload-row,.document-row{display:flex;align-items:center;gap:.7rem;border:1px solid rgba(148,163,184,.25);border-radius:.8rem;padding:.65rem}.upload-row progress{flex:1}.upload-row small{font-size:.7rem;color:#64748b}.document-list{display:flex;flex-direction:column;gap:.45rem;max-height:25rem;overflow:auto}.file-icon{display:flex;width:2.6rem;height:2.6rem;align-items:center;justify-content:center;border-radius:.65rem;background:rgba(20,184,166,.1);font-size:.6rem;font-weight:900;color:#0f766e}.filter{border-radius:999px;padding:.3rem .6rem;font-size:.72rem}.filter.active{background:#0d9488;color:white}@media(max-width:767px){.document-row{align-items:flex-start;flex-wrap:wrap}.document-row .min-w-0{min-width:calc(100% - 4rem)}}</style>
