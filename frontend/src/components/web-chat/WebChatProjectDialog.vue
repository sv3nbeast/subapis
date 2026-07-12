<template>
  <BaseDialog :show="show" :title="project ? t('webChat.editProject') : t('webChat.newProject')" @close="emit('close')">
    <div class="space-y-4">
      <label class="field"><span>{{ t('common.name') }}</span><input v-model="form.name" class="input" maxlength="120" /></label>
      <label class="field"><span>{{ t('common.description') }}</span><textarea v-model="form.description" class="input" rows="3" maxlength="500" /></label>
      <div class="grid grid-cols-2 gap-3">
        <label class="field"><span>{{ t('webChat.projectColor') }}</span><input v-model="form.color" class="input h-10" type="color" /></label>
        <label class="field"><span>{{ t('webChat.sortOrder') }}</span><input v-model.number="form.sort_order" class="input" type="number" /></label>
      </div>
      <label class="field"><span>{{ t('webChat.defaultGroup') }}</span><select v-model="groupValue" class="input"><option value="">{{ t('webChat.noDefault') }}</option><option v-for="g in groups" :key="g.id" :value="String(g.id)">{{ g.name }}</option></select></label>
      <label class="field"><span>{{ t('webChat.defaultModel') }}</span><select v-model="form.default_model" class="input" :disabled="!selectedGroup"><option value="">{{ t('webChat.noDefault') }}</option><option v-for="m in selectedGroup?.models || []" :key="m.name" :value="m.name">{{ m.name }}</option></select></label>
      <label class="field"><span>{{ t('webChat.defaultTemplate') }}</span><select v-model="templateValue" class="input"><option value="">{{ t('webChat.noDefault') }}</option><option v-for="item in templates" :key="item.id" :value="String(item.id)">{{ item.name }}</option></select></label>
      <p v-if="error" class="text-sm text-red-500">{{ error }}</p>
    </div>
    <template #footer><div class="flex justify-between gap-3"><button v-if="project" class="btn btn-secondary text-red-600" :disabled="saving" @click="remove">{{ t('common.delete') }}</button><span v-else/><div class="flex gap-3"><button class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="saving || !form.name.trim()" @click="save">{{ t('common.save') }}</button></div></div></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import webChatAPI, { type WebChatGroupOption, type WebChatProject, type WebChatProjectInput, type WebChatTemplate } from '@/api/webChat'
import { extractApiErrorMessage } from '@/utils/apiError'
const props=defineProps<{show:boolean;project:WebChatProject|null;groups:WebChatGroupOption[];templates:WebChatTemplate[]}>()
const emit=defineEmits<{close:[];saved:[WebChatProject];deleted:[number]}>();const{t}=useI18n();const saving=ref(false),error=ref('')
const form=reactive<WebChatProjectInput>({name:'',description:'',color:'#14b8a6',sort_order:0,default_group_id:null,default_model:'',default_template_id:null})
const groupValue=computed({get:()=>form.default_group_id?String(form.default_group_id):'',set:v=>{form.default_group_id=v?Number(v):null;if(!selectedGroup.value?.models.some(m=>m.name===form.default_model))form.default_model=''}})
const templateValue=computed({get:()=>form.default_template_id?String(form.default_template_id):'',set:v=>{form.default_template_id=v?Number(v):null}})
const selectedGroup=computed(()=>props.groups.find(g=>g.id===form.default_group_id)||null)
watch(()=>[props.show,props.project] as const,()=>{if(!props.show)return;Object.assign(form,{name:props.project?.name||'',description:props.project?.description||'',color:props.project?.color||'#14b8a6',sort_order:props.project?.sort_order||0,default_group_id:props.project?.default_group_id??null,default_model:props.project?.default_model||'',default_template_id:props.project?.default_template_id??null});error.value=''},{immediate:true})
async function save(){saving.value=true;error.value='';try{const item=props.project?await webChatAPI.patchProject(props.project.id,{...form}):await webChatAPI.createProject({...form});emit('saved',item)}catch(e){error.value=extractApiErrorMessage(e)}finally{saving.value=false}}
async function remove(){if(!props.project||!confirm(t('webChat.deleteProjectConfirm')))return;saving.value=true;try{await webChatAPI.deleteProject(props.project.id);emit('deleted',props.project.id)}catch(e){error.value=extractApiErrorMessage(e)}finally{saving.value=false}}
</script>
<style scoped>.field{display:flex;flex-direction:column;gap:.35rem;font-size:.78rem;font-weight:700;color:#64748b}</style>
