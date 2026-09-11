<template>
  <section class="card p-6" aria-labelledby="web-chat-model-catalog-title">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div><h2 id="web-chat-model-catalog-title" class="text-lg font-semibold">{{ text.title }}</h2><p class="mt-1 text-sm text-gray-500">{{ text.hint }}</p></div>
      <button type="button" class="btn btn-secondary" :disabled="busy || loading || !loaded" @click="add">+ {{ text.add }}</button>
    </div>
    <p class="my-3 rounded-lg bg-amber-50 p-3 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-200">{{ text.safety }}</p>
    <p v-if="loading">{{ text.loading }}</p>
    <div v-for="(entry,index) in entries" :key="entry.id" class="mb-4 space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
      <div class="flex flex-wrap items-center justify-between gap-3"><strong>{{ entry.name || text.add }}</strong><div class="flex gap-3"><label><input v-model="entry.enabled" type="checkbox" :disabled="busy" /> {{ text.enabled }}</label><label><input :checked="entry.recommended" type="checkbox" :disabled="busy" @change="recommend(index,($event.target as HTMLInputElement).checked)" /> {{ text.recommend }}</label><button type="button" :disabled="busy" class="text-red-600" @click="remove(index)">{{ text.remove }}</button></div></div>
      <div class="grid gap-3 md:grid-cols-2">
        <label class="catalog-field">{{ text.name }}<input v-model="entry.name" maxlength="100" class="input" :disabled="busy" /></label>
        <label class="catalog-field">{{ text.brand }}<input v-model="entry.brand" maxlength="40" class="input" :disabled="busy" placeholder="Claude / OpenAI / Grok" /></label>
        <label class="catalog-field">{{ text.group }}<select v-model.number="entry.group_id" class="input" :disabled="busy"><option :value="0">{{ text.choose }}</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }} · {{ g.platform }} · {{ g.subscription_type }}</option></select></label>
        <label class="catalog-field">{{ text.model }}<input v-model="entry.model" maxlength="200" class="input font-mono" :disabled="busy" placeholder="claude-opus-5" /></label>
        <label class="catalog-field">{{ text.description }}<input v-model="entry.description" maxlength="300" class="input" :disabled="busy" /></label>
        <label class="catalog-field">{{ text.order }}<input v-model.number="entry.sort_order" type="number" class="input" :disabled="busy" /></label>
      </div>
    </div>
    <p v-if="!loading && !entries.length" class="my-4 text-sm text-gray-500">{{ text.empty }}</p>
    <p v-if="error" role="alert" class="my-3 text-sm text-red-600">{{ error }}</p>
    <p v-if="notice" role="status" class="my-3 text-sm text-primary-600">{{ notice }}</p>
    <div class="flex gap-3"><button type="button" class="btn btn-secondary" :disabled="busy || loading || !loaded" @click="validate">{{ text.validate }}</button><button type="button" class="btn btn-primary" :disabled="busy || loading || !loaded" @click="save">{{ text.save }}</button></div>
  </section>
</template>
<script setup lang="ts">
import { computed,onMounted,ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAll } from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { getModelCatalog,saveModelCatalog,validateModelCatalog,type WebChatCatalogEntry } from '@/api/webChat'
import { extractApiErrorMessage } from '@/utils/apiError'
const {locale}=useI18n()
const text=computed(()=>locale.value.startsWith('zh')?{
  title:'网页对话 · 模型目录',hint:'用户只选择模型；这里为每个模型绑定唯一的实际分组。新增版本请先配置校验，再发布。',safety:'不会授予额外分组权限，不会自动切换收费方式。仅支持浏览器可用分组；仅限 Claude Code 的分组不可用。删除或改绑后，旧选择必须重新选择。',add:'添加模型',loading:'加载中…',enabled:'启用',recommend:'推荐',remove:'删除',name:'显示名称',brand:'品牌',group:'使用分组（仅管理员可见）',choose:'选择分组',model:'实际模型 ID',description:'简介',order:'排序',empty:'尚未发布模型目录。请添加已验证的模型和分组，用户端才会出现可选模型。',validate:'校验配置（不发起付费调用）',save:'发布模型目录',valid:'配置校验通过。仅验证配置，不代表上游实时可用。',saved:'模型目录已发布。',confirm:'确认发布？改绑或删除模型会影响已有会话的后续请求。',removeConfirm:'从待发布目录移除此模型？'
}:{title:'Web Chat · Model catalog',hint:'Users choose models. Each entry resolves to one explicit group. Validate before publishing.',safety:'Does not grant group access or switch billing modes. CLI-only groups cannot be used. Rerouting requires a new selection.',add:'Add model',loading:'Loading…',enabled:'Enabled',recommend:'Recommended',remove:'Remove',name:'Display name',brand:'Brand',group:'Group (admin only)',choose:'Choose group',model:'Actual model ID',description:'Description',order:'Sort order',empty:'No catalog published. Add validated routes to make models available.',validate:'Validate configuration (no billable requests)',save:'Publish catalog',valid:'Configuration valid. This is not a live upstream health check.',saved:'Catalog published.',confirm:'Publish? Rerouting or removal affects subsequent requests in existing conversations.',removeConfirm:'Remove this model from the draft catalog?'})
const entries=ref<WebChatCatalogEntry[]>([]),groups=ref<AdminGroup[]>([]),loaded=ref(false),loading=ref(true),busy=ref(false),error=ref(''),notice=ref('')
onMounted(async()=>{try{const[cfg,all]=await Promise.all([getModelCatalog(),getAll()]);entries.value=cfg.entries||[];groups.value=all.filter(g=>!g.claude_code_only);loaded.value=true}catch(e){error.value=extractApiErrorMessage(e)}finally{loading.value=false}})
function add(){entries.value.push({id:crypto.randomUUID(),name:'',brand:'',description:'',group_id:0,model:'',enabled:true,recommended:false,sort_order:entries.value.length*10})}
function recommend(index:number,value:boolean){entries.value.forEach((e,i)=>{e.recommended=i===index&&value})}
function remove(index:number){if(confirm(text.value.removeConfirm))entries.value.splice(index,1)}
async function validate(){busy.value=true;error.value='';notice.value='';try{await validateModelCatalog({entries:entries.value});notice.value=text.value.valid}catch(e){error.value=extractApiErrorMessage(e)}finally{busy.value=false}}
async function save(){if(!confirm(text.value.confirm))return;busy.value=true;error.value='';notice.value='';try{const cfg=await saveModelCatalog({entries:entries.value});entries.value=cfg.entries;notice.value=text.value.saved}catch(e){error.value=extractApiErrorMessage(e)}finally{busy.value=false}}
</script>
<style scoped>.catalog-field{display:flex;flex-direction:column;gap:.3rem;font-size:.8rem;color:#64748b}</style>
