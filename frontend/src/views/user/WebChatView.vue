<template>
  <AppLayout>
    <div ref="webChatShellRef" class="web-chat-shell" :style="webChatShellStyle">
      <aside class="session-panel" :class="{ 'session-panel-open': sessionsOpen }">
        <div class="flex items-center justify-between">
          <div><p class="eyebrow">{{ t('webChat.eyebrow') }}</p><h2 class="text-lg font-black">{{ t('webChat.sessions') }}</h2></div>
          <button class="icon-button session-toggle" :aria-label="t('common.close')" @click="sessionsOpen = false"><Icon name="x" size="sm" /></button>
        </div>
        <button class="new-chat" :disabled="!enabled || !hasUsableModel || sending" @click="startDraftSession">
          <Icon name="plus" size="sm" /> {{ t('webChat.newChat') }}
        </button>
        <div class="search-box"><Icon name="search" size="sm" /><input v-model="sessionQuery" :placeholder="t('webChat.searchSessions')" /></div>
        <div v-if="options.projects_enabled" class="project-nav">
          <div class="flex items-center justify-between"><span class="eyebrow">{{ t('webChat.projects') }}</span><button class="project-add" :title="t('webChat.newProject')" @click="openProjectEditor(null)">+</button></div>
          <button class="project-item" :class="{active:projectFilter==='all'}" @click="projectFilter='all'">{{ t('webChat.allChats') }} <span>{{ sessions.length }}</span></button>
          <button class="project-item" :class="{active:projectFilter===null}" @click="projectFilter=null">{{ t('webChat.uncategorized') }} <span>{{ sessions.filter(s=>!s.project_id).length }}</span></button>
          <button v-for="project in projects" :key="project.id" class="project-item" :class="{active:projectFilter===project.id}" @click="projectFilter=project.id"><i :style="{background:project.color}"/><b>{{ project.name }}</b><span>{{ project.session_count }}</span><em v-if="options.files_enabled" :title="t('webChat.knowledgeLibrary')" @click.stop="openKnowledgeLibrary(project)">▣</em><em @click.stop="openProjectEditor(project)">•••</em></button>
        </div>
        <div class="session-list">
          <article v-for="session in displayedSessions" :key="session.id" class="session-item" :class="{ active: session.id === activeSessionId }">
            <button class="min-w-0 flex-1 text-left" @click="selectSession(session)">
              <div class="flex items-center gap-1.5"><span v-if="session.pinned_at" class="pin-dot">◆</span><strong class="truncate text-sm">{{ session.title || session.model }}</strong></div>
              <p class="mt-1 truncate text-xs text-gray-500">{{ session.group_name || groupName(session.group_id) }} · {{ session.model }}</p>
            </button>
            <div class="session-actions">
              <button :title="t('webChat.pin')" @click="togglePin(session)">◆</button>
              <button :title="t('webChat.rename')" @click="renameSession(session)">✎</button>
            </div>
          </article>
          <p v-if="!displayedSessions.length && !loading" class="empty-small">{{ t('webChat.noSessions') }}</p>
        </div>
      </aside>

      <div class="resize-handle" role="separator" aria-orientation="vertical" :aria-label="t('webChat.resizeSessions')" @pointerdown="startPanelResize('session', $event)" @dblclick="resetPanelWidth('session')" />

      <main class="chat-panel">
        <header class="chat-header">
          <button class="icon-button session-toggle" :aria-label="t('webChat.openSessions')" @click="sessionsOpen = true"><Icon name="menu" size="sm" /></button>
          <div class="min-w-0 flex-1"><p class="text-xs text-primary-600">{{ selectedGroup?.name || t('webChat.selectGroup') }}</p><h1 class="truncate text-xl font-black">{{ activeSession?.title || activeSession?.model || t('webChat.title') }}</h1></div>
          <div v-if="activeSession" class="header-actions">
            <button class="icon-button" :title="t('webChat.exportMarkdown')" @click="exportConversation('markdown')"><Icon name="download" size="sm" /><span>MD</span></button>
            <button class="icon-button" :title="t('webChat.exportJson')" @click="exportConversation('json')"><Icon name="download" size="sm" /><span>JSON</span></button>
            <button class="icon-button danger" :title="t('common.delete')" :disabled="sending" @click="removeCurrentSession"><Icon name="trash" size="sm" /></button>
          </div>
          <button class="icon-button context-toggle" :aria-label="t('webChat.openContext')" @click="contextOpen = true"><Icon name="cog" size="sm" /></button>
        </header>

        <section ref="messageListRef" class="message-list">
          <div v-if="!enabled" class="empty-state"><Icon name="lock" size="xl"/><h2>{{ t('webChat.disabledTitle') }}</h2><p>{{ t('webChat.disabledDescription') }}</p></div>
          <div v-else-if="loading" class="empty-state"><Icon name="refresh" size="xl" class="animate-spin"/><h2>{{ t('common.loading') }}</h2></div>
          <div v-else-if="!hasUsableModel" class="empty-state"><Icon name="inbox" size="xl"/><h2>{{ t('webChat.noGroupsTitle') }}</h2><p>{{ t('webChat.noGroupsDescription') }}</p></div>
          <div v-else-if="!activeSession || messages.length === 0" class="empty-state">
            <Icon name="chat" size="xl"/><h2>{{ t('webChat.emptyTitle') }}</h2><p>{{ t('webChat.emptyDescription') }}</p>
            <div class="quick-prompts"><button v-for="prompt in quickPrompts" :key="prompt" @click="draft = prompt">{{ prompt }}</button></div>
          </div>

          <template v-for="message in messages" :key="message.id">
            <div class="message-row" :class="message.role">
              <div class="message-stack">
                <div class="message-bubble" :class="message.role">
                  <WebChatMessageContent :content="message.content" :markdown="message.role === 'assistant'" />
                  <p v-if="message.status === 'error' || message.status === 'partial'" class="message-error">{{ message.error_message || t('webChat.streamError') }}</p>
                </div>
                <div class="message-footer">
                  <span>{{ formatMessageTime(message.updated_at || message.created_at) }}</span>
                  <template v-if="message.role === 'assistant' && hasUsage(message)">
                    <span>↑{{ formatTokens(message.input_tokens) }}</span><span>↓{{ formatTokens(message.output_tokens) }}</span>
                    <span v-if="message.cache_read_tokens">R {{ formatTokens(message.cache_read_tokens) }}</span><span v-if="message.cache_creation_tokens">W {{ formatTokens(message.cache_creation_tokens) }}</span>
                    <span v-if="estimatedCost(message) !== null">≈ ${{ estimatedCost(message)!.toFixed(6) }}</span>
                  </template>
                  <span v-if="message.request_id" :title="message.request_id">ID {{ shortRequestID(message.request_id) }}</span>
                  <span v-if="options.history_enabled && message.version_count > 1" class="version-switch"><button :disabled="message.version_index<=1||sending" @click="switchVersion(message,-1)">‹</button>{{ message.version_index }} / {{ message.version_count }}<button :disabled="message.version_index>=message.version_count||sending" @click="switchVersion(message,1)">›</button><small>{{ versionReason(message.version_reason) }}</small></span>
                </div>
                <div class="message-actions">
                  <button @click="copyText(message.content)"><Icon name="copy" size="xs"/>{{ t('webChat.copy') }}</button>
                  <button @click="quoteMessage(message)"><Icon name="chatBubble" size="xs"/>{{ t('webChat.quote') }}</button>
                  <button v-if="message.role === 'user'" :disabled="sending" @click="reviseMessage(message)"><Icon name="edit" size="xs"/>{{ t('webChat.editResend') }}</button>
                  <button v-else :disabled="sending" @click="regenerateMessage(message)"><Icon name="refresh" size="xs"/>{{ message.status === 'error' ? t('webChat.retry') : t('webChat.regenerate') }}</button>
                </div>
                <WebChatSources :sources="message.sources || []" />
              </div>
            </div>
          </template>

          <div v-if="sending" class="message-row assistant"><div class="message-stack"><div class="message-bubble assistant">
            <WebChatMessageContent v-if="streamingText" :content="streamingText" markdown />
            <span v-else class="typing">{{ t('webChat.thinking') }} ···</span>
          </div></div></div>
        </section>

        <WebChatComposer v-model="draft" :disabled="!canCompose" :can-send="canSend&&Boolean(draft.trim())" :sending="sending" :files-enabled="options.files_enabled" :templates-enabled="options.templates_enabled" :template-name="activeTemplateName" :documents="pendingDocuments" :failed-attachments="failedAttachments" :attachment-state="attachmentState" @submit="send" @stop="stopGeneration" @open-template="templateDialogOpen=true" @files="uploadTemporaryDocuments" @remove-document="removePendingDocument" @retry-attachment="retryFailedAttachment" @remove-failed-attachment="removeFailedAttachment"/>
      </main>

      <div class="resize-handle" role="separator" aria-orientation="vertical" :aria-label="t('webChat.resizeContext')" @pointerdown="startPanelResize('context', $event)" @dblclick="resetPanelWidth('context')" />

      <aside class="context-panel" :class="{ 'context-panel-open': contextOpen }">
        <div class="context-card">
          <div class="flex items-center justify-between">
            <p class="context-label">{{ t('webChat.context') }}</p>
            <button class="icon-button context-toggle" :aria-label="t('common.close')" @click="contextOpen = false"><Icon name="x" size="sm" /></button>
          </div>
          <label>{{ t('webChat.group') }}</label><select v-model.number="selectedGroupId" class="input" :disabled="sending"><option v-for="group in options.groups" :key="group.id" :value="group.id">{{ group.name }} · {{ platformLabel(group.platform) }}</option></select>
          <label>{{ t('webChat.model') }}</label><select v-model="selectedModel" class="input" :disabled="sending"><option v-for="model in selectedGroupModels" :key="model.name" :value="model.name">{{ model.name }}</option></select>
          <template v-if="options.projects_enabled && activeSession"><label>{{ t('webChat.project') }}</label><select :value="activeSession.project_id || ''" class="input" :disabled="sending" @change="moveActiveSession"><option value="">{{ t('webChat.uncategorized') }}</option><option v-for="project in projects" :key="project.id" :value="project.id">{{ project.name }}</option></select></template>
          <label v-if="options.files_enabled && activeSession" class="knowledge-toggle"><span>{{ t('webChat.useProjectKnowledge') }}</span><input type="checkbox" :checked="activeSession.knowledge_enabled" :disabled="sending" @change="toggleKnowledge"/></label>
          <WebChatSources v-if="latestSources.length" :sources="latestSources" />
          <button class="advanced-toggle" @click="advancedOpen = !advancedOpen"><Icon name="cog" size="sm"/>{{ t('webChat.advanced') }}<Icon :name="advancedOpen ? 'chevronUp' : 'chevronDown'" size="xs"/></button>
          <div v-if="advancedOpen" class="advanced-settings">
            <label>{{ t('webChat.systemPrompt') }} <span>{{ systemPrompt.length }}/8000</span></label><textarea v-model="systemPrompt" rows="5" maxlength="8000" class="input" />
            <label>{{ t('webChat.temperature') }}</label><input v-model="temperatureInput" class="input" type="number" min="0" max="2" step="0.1" :placeholder="t('webChat.temperatureDefault')" />
            <label>{{ t('webChat.maxOutputTokens') }}</label><input v-model.number="maxOutputTokens" class="input" type="number" min="1" max="32768" />
            <button class="save-settings" :disabled="!activeSession || savingSettings" @click="saveAdvancedSettings">{{ t('common.save') }}</button>
          </div>
          <div class="pricing-card"><p class="pricing-title">{{ t('webChat.priceHint') }}</p><div v-if="pricingItems.length" class="pricing-grid"><div v-for="item in pricingItems" :key="item.label"><span>{{ item.label }}</span><strong>{{ item.value }}</strong></div></div><p v-else class="text-xs text-gray-500">{{ t('webChat.noPricing') }}</p></div>
        </div>
      </aside>
      <button v-if="sessionsOpen || contextOpen" class="panel-scrim" :aria-label="t('common.close')" @click="sessionsOpen = false; contextOpen = false" />
    </div>
    <WebChatProjectDialog :show="projectDialogOpen" :project="editingProject" :groups="options.groups" :templates="templates" @close="projectDialogOpen=false" @saved="onProjectSaved" @deleted="onProjectDeleted"/>
    <WebChatTemplateDialog :show="templateDialogOpen" :templates="localizedTemplates" @close="templateDialogOpen=false" @apply="applyTemplate" @changed="loadTemplates"/>
    <WebChatKnowledgeLibrary :show="knowledgeLibraryOpen" :project="knowledgeProject" :limits="options.file_limits" @close="knowledgeLibraryOpen=false"/>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import WebChatMessageContent from '@/components/web-chat/WebChatMessageContent.vue'
import WebChatProjectDialog from '@/components/web-chat/WebChatProjectDialog.vue'
import WebChatTemplateDialog from '@/components/web-chat/WebChatTemplateDialog.vue'
import WebChatKnowledgeLibrary from '@/components/web-chat/WebChatKnowledgeLibrary.vue'
import WebChatSources from '@/components/web-chat/WebChatSources.vue'
import WebChatComposer from '@/components/web-chat/WebChatComposer.vue'
import webChatAPI, { type WebChatMessage, type WebChatOptions, type WebChatProject, type WebChatSession, type WebChatSource, type WebChatStreamHandlers, type WebChatTemplate } from '@/api/webChat'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import { formatScaled } from '@/utils/pricing'
import { selectLocalizedWebChatTemplates } from '@/utils/webChatTemplates'
import { useWebChatDocuments } from '@/composables/useWebChatDocuments'

const { t, locale } = useI18n(); const authStore = useAuthStore()
const loading=ref(true), creatingSession=ref(false), sending=ref(false), savingSettings=ref(false), enabled=ref(false), sessionsOpen=ref(false), contextOpen=ref(false), advancedOpen=ref(false)
const projectDialogOpen=ref(false),templateDialogOpen=ref(false),editingProject=ref<WebChatProject|null>(null),projectFilter=ref<'all'|number|null>('all')
const knowledgeLibraryOpen=ref(false),knowledgeProject=ref<WebChatProject|null>(null),streamingSources=ref<WebChatSource[]>([])
const options=ref<WebChatOptions>({enabled:false,groups:[],projects_enabled:false,templates_enabled:false,history_enabled:false,files_enabled:false,file_limits:{max_file_bytes:20*1024*1024,max_files_per_project:50,max_bytes_per_user:500*1024*1024}}), sessions=ref<WebChatSession[]>([]), messages=ref<WebChatMessage[]>([]),projects=ref<WebChatProject[]>([]),templates=ref<WebChatTemplate[]>([])
const activeTemplateId=ref<number|null>(null)
const activeSessionId=ref<number|null>(null), selectedGroupId=ref<number|null>(null), selectedModel=ref(''), draft=ref(''), streamingText=ref(''), sessionQuery=ref('')
const systemPrompt=ref(''), temperatureInput=ref(''), maxOutputTokens=ref(8192), messageListRef=ref<HTMLElement|null>(null)
const abortController=ref<AbortController|null>(null); let searchTimer:ReturnType<typeof setTimeout>|null=null
const webChatShellRef=ref<HTMLElement|null>(null), resizingPanel=ref<'session'|'context'|null>(null), sessionPanelWidth=ref(256), contextPanelWidth=ref(288)
const {pendingDocuments,failedAttachments,attachmentState,uploadTemporaryDocuments,retryFailedAttachment,removePendingDocument,removeFailedAttachment,clearPendingDocuments,discardPendingDocuments}=useWebChatDocuments(async()=>activeSession.value||await createSessionForCurrentSelection(),key=>t(key))

const activeSession=computed(()=>sessions.value.find(item=>item.id===activeSessionId.value)||null)
const selectedGroup=computed(()=>options.value.groups.find(group=>group.id===selectedGroupId.value)||null)
const selectedGroupModels=computed(()=>selectedGroup.value?.models||[])
const selectedModelOption=computed(()=>selectedGroupModels.value.find(model=>model.name===selectedModel.value)||null)
const hasUsableModel=computed(()=>options.value.groups.some(group=>group.models.length>0))
const displayedSessions=computed(()=>projectFilter.value==='all'?sessions.value:sessions.value.filter(s=>(s.project_id??null)===projectFilter.value))
const selectedProject=computed(()=>typeof projectFilter.value==='number'?projects.value.find(p=>p.id===projectFilter.value)||null:null)
const activeTemplateName=computed(()=>templates.value.find(x=>x.id===activeTemplateId.value)?.name||'')
const localizedTemplates=computed(()=>selectLocalizedWebChatTemplates(templates.value,locale.value))
const latestSources=computed(()=>streamingSources.value.length?streamingSources.value:[...(messages.value)].reverse().find(m=>m.sources?.length)?.sources||[])
const canCompose=computed(()=>enabled.value&&hasUsableModel.value&&!creatingSession.value)
const canSend=computed(()=>canCompose.value&&!sending.value&&Boolean(selectedGroup.value&&selectedModel.value)&&draft.value.length<=20000)
const quickPrompts=computed(()=>[t('webChat.promptSummarize'),t('webChat.promptExplain'),t('webChat.promptPlan')])
const webChatShellStyle=computed(()=>({'--session-width':`${sessionPanelWidth.value}px`,'--context-width':`${contextPanelWidth.value}px`}))
const pricingItems=computed(()=>{const p=selectedModelOption.value?.pricing;if(!p)return[];if(p.billing_mode==='per_request')return[{label:t('webChat.perRequest'),value:formatScaled(p.per_request_price??null,1)}];return[
  {label:t('webChat.inputPrice'),value:formatScaled(p.input_price??null,1_000_000)},{label:t('webChat.outputPrice'),value:formatScaled(p.output_price??null,1_000_000)},
  {label:t('webChat.cacheWritePrice'),value:formatScaled(p.cache_write_price??null,1_000_000)},{label:t('webChat.cacheReadPrice'),value:formatScaled(p.cache_read_price??null,1_000_000)}].filter(i=>i.value!=='-')})

watch(selectedGroupId,()=>{if(!selectedGroupModels.value.some(m=>m.name===selectedModel.value))selectedModel.value=selectedGroupModels.value[0]?.name||''})
watch(sessionQuery,()=>{if(searchTimer)clearTimeout(searchTimer);searchTimer=setTimeout(()=>void refreshSessions(),250)})
watch(draft,value=>localStorage.setItem(draftKey(activeSessionId.value),value))
watch(activeSessionId,id=>{draft.value=localStorage.getItem(draftKey(id))||''})
watch(projectFilter,()=>{if(!activeSession.value)startDraftSession()})
onMounted(()=>{restorePanelWidths();void loadInitial()})
onBeforeUnmount(()=>{abortController.value?.abort();if(searchTimer)clearTimeout(searchTimer);stopPanelResize();void discardPendingDocuments()})

async function loadInitial(){loading.value=true;try{const[opts,list]=await Promise.all([webChatAPI.getOptions(),webChatAPI.listSessions()]);options.value=opts;enabled.value=opts.enabled;sessions.value=list;selectedGroupId.value=opts.default_group_id??opts.groups[0]?.id??null;selectedModel.value=opts.default_model||opts.groups[0]?.models[0]?.name||'';await Promise.all([opts.projects_enabled?loadProjects():Promise.resolve(),opts.templates_enabled?loadTemplates():Promise.resolve()]);if(list[0])await selectSession(list[0])}catch(e){console.error(extractApiErrorMessage(e))}finally{loading.value=false}}
async function refreshSessions(){sessions.value=await webChatAPI.listSessions(sessionQuery.value).catch(()=>sessions.value)}
async function loadProjects(){if(!options.value.projects_enabled)return;projects.value=await webChatAPI.listProjects().catch(()=>projects.value)}
async function loadTemplates(){if(!options.value.templates_enabled)return;templates.value=await webChatAPI.listTemplates().catch(()=>templates.value)}
function openProjectEditor(project:WebChatProject|null){editingProject.value=project;projectDialogOpen.value=true}
function openKnowledgeLibrary(project:WebChatProject){knowledgeProject.value=project;knowledgeLibraryOpen.value=true}
function onProjectSaved(project:WebChatProject){const index=projects.value.findIndex(p=>p.id===project.id);if(index>=0)projects.value[index]=project;else projects.value.push(project);projects.value.sort((a,b)=>a.sort_order-b.sort_order);projectDialogOpen.value=false;projectFilter.value=project.id}
function onProjectDeleted(id:number){projects.value=projects.value.filter(p=>p.id!==id);sessions.value.forEach(s=>{if(s.project_id===id){s.project_id=null;s.project_name=''}});if(projectFilter.value===id)projectFilter.value=null;projectDialogOpen.value=false}
function applyTemplate(content:string,templateID:number){draft.value=content;activeTemplateId.value=templateID;templateDialogOpen.value=false;void nextTick(()=>document.querySelector<HTMLTextAreaElement>('.composer-input')?.focus())}
async function startDraftSession(){await discardPendingDocuments();activeSessionId.value=null;messages.value=[];streamingText.value='';streamingSources.value=[];sessionsOpen.value=false;const p=selectedProject.value;if(p?.default_group_id)selectedGroupId.value=p.default_group_id;if(p?.default_model)selectedModel.value=p.default_model;activeTemplateId.value=p?.default_template_id??null}
async function createSessionForCurrentSelection(){if(!selectedGroupId.value||!selectedModel.value)return null;creatingSession.value=true;try{const s=await webChatAPI.createSession({group_id:selectedGroupId.value,model:selectedModel.value,project_id:selectedProject.value?.id??null,default_template_id:activeTemplateId.value});sessions.value=[s,...sessions.value];activeSessionId.value=s.id;applySessionSettings(s);if(options.value.projects_enabled)await loadProjects();return s}finally{creatingSession.value=false}}
async function selectSession(session:WebChatSession){if(sending.value)return;if(activeSessionId.value!==session.id)await discardPendingDocuments();activeSessionId.value=session.id;selectedGroupId.value=session.group_id;selectedModel.value=session.model;activeTemplateId.value=session.default_template_id??null;applySessionSettings(session);sessionsOpen.value=false;messages.value=await webChatAPI.listMessages(session.id);await scrollToBottom()}
function applySessionSettings(session:WebChatSession){systemPrompt.value=session.system_prompt||'';temperatureInput.value=session.temperature==null?'':String(session.temperature);maxOutputTokens.value=session.max_output_tokens||8192}
async function renameSession(session:WebChatSession){const title=window.prompt(t('webChat.renamePrompt'),session.title||session.model)?.trim();if(!title)return;const updated=await webChatAPI.patchSession(session.id,{title});Object.assign(session,updated)}
async function togglePin(session:WebChatSession){await webChatAPI.patchSession(session.id,{pinned:!session.pinned_at});await refreshSessions()}
async function moveActiveSession(event:Event){const session=activeSession.value;if(!session)return;const raw=(event.target as HTMLSelectElement).value;const updated=await webChatAPI.patchSession(session.id,{project_id:raw?Number(raw):null});Object.assign(session,updated);await loadProjects()}
async function removeCurrentSession(){const s=activeSession.value;if(!s||!window.confirm(t('webChat.deleteConfirm')))return;await webChatAPI.deleteSession(s.id);localStorage.removeItem(draftKey(s.id));activeSessionId.value=null;messages.value=[];await refreshSessions();if(sessions.value[0])await selectSession(sessions.value[0])}
async function saveAdvancedSettings(){const s=activeSession.value;if(!s)return;const temp=temperatureInput.value.trim()===''?null:Number(temperatureInput.value);if(temp!==null&&(!Number.isFinite(temp)||temp<0||temp>2)){window.alert(t('webChat.invalidTemperature'));return}if(maxOutputTokens.value<1||maxOutputTokens.value>32768){window.alert(t('webChat.invalidMaxTokens'));return}savingSettings.value=true;try{const updated=await webChatAPI.patchSession(s.id,{system_prompt:systemPrompt.value,temperature:temp,max_output_tokens:maxOutputTokens.value});Object.assign(s,updated)}finally{savingSettings.value=false}}
async function toggleKnowledge(event:Event){const s=activeSession.value;if(!s)return;const updated=await webChatAPI.patchSession(s.id,{knowledge_enabled:(event.target as HTMLInputElement).checked});Object.assign(s,updated)}


async function send(){const content=draft.value.trim();if(!canSend.value||!content)return;const session=activeSession.value||await createSessionForCurrentSelection();if(!session)return;localStorage.removeItem(draftKey(session.id));draft.value='';const templateID=activeTemplateId.value;activeTemplateId.value=null;const documentIDs=pendingDocuments.value.map(d=>d.id);await runGeneration(handlers=>webChatAPI.streamMessage(session.id,{content,group_id:selectedGroupId.value,model:selectedModel.value,template_id:templateID,knowledge_enabled:session.knowledge_enabled,document_ids:documentIDs},handlers),{role:'user',content},clearPendingDocuments)}
async function regenerateMessage(message:WebChatMessage){const s=activeSession.value;if(!s)return;await runGeneration(handlers=>webChatAPI.regenerateMessage(s.id,message.id,handlers))}
async function reviseMessage(message:WebChatMessage){const content=window.prompt(t('webChat.revisePrompt'),message.content)?.trim();const s=activeSession.value;if(!s||!content||content===message.content)return;await runGeneration(handlers=>webChatAPI.reviseMessage(s.id,message.id,content,handlers))}
async function switchVersion(message:WebChatMessage,direction:-1|1){const s=activeSession.value;if(!s||sending.value)return;const versions=await webChatAPI.listMessageVersions(s.id,message.id);const current=versions.findIndex(v=>v.id===message.id);const target=versions[current+direction];if(!target)return;messages.value=await webChatAPI.activateMessageVersion(s.id,target.id);await scrollToBottom()}
function versionReason(reason:WebChatMessage['version_reason']){return reason==='regenerate'?t('webChat.versionRegenerated'):reason==='edit'?t('webChat.versionEdited'):t('webChat.versionOriginal')}
async function runGeneration(request:(handlers:WebChatStreamHandlers)=>Promise<void>,optimistic?:{role:'user';content:string},onAccepted?:()=>void){const sessionID=activeSessionId.value;if(!sessionID||sending.value)return;if(optimistic)messages.value.push(localMessage(sessionID,optimistic.role,optimistic.content));sending.value=true;streamingText.value='';streamingSources.value=[];abortController.value=new AbortController();let accepted=false;await scrollToBottom();try{await request({signal:abortController.value.signal,onMeta(){if(!accepted){accepted=true;onAccepted?.()}},onDelta(text){streamingText.value+=text;void scrollToBottom()},onSources(sources){streamingSources.value=sources}})}catch(e){if((e as Error).name!=='AbortError')console.error(extractApiErrorMessage(e))}finally{sending.value=false;abortController.value=null;streamingText.value='';await new Promise(resolve=>setTimeout(resolve,100));messages.value=await webChatAPI.listMessages(sessionID).catch(()=>messages.value);streamingSources.value=[];await refreshSessions();activeSessionId.value=sessionID;await scrollToBottom()}}
function stopGeneration(){abortController.value?.abort()}
function localMessage(sessionID:number,role:'user'|'assistant',content:string):WebChatMessage{const now=new Date().toISOString(),id=-Date.now();return{id,session_id:sessionID,user_id:authStore.user?.id||0,role,content,status:'completed',input_tokens:0,output_tokens:0,cache_read_tokens:0,cache_creation_tokens:0,logical_id:id,version_index:1,version_count:1,version_reason:'original',sources:[],created_at:now,updated_at:now}}
function quoteMessage(message:WebChatMessage){draft.value=`${message.content.split('\n').map(line=>`> ${line}`).join('\n')}\n\n${draft.value}`;void nextTick(()=>document.querySelector<HTMLTextAreaElement>('.composer-input')?.focus())}
async function copyText(text:string){await navigator.clipboard.writeText(text)}
function exportConversation(format:'markdown'|'json'){const s=activeSession.value;if(!s)return;const content=format==='json'?JSON.stringify({session:s,messages:messages.value},null,2):[`# ${s.title||s.model}`,`> ${s.group_name||groupName(s.group_id)} · ${s.model}`,'',...messages.value.flatMap(m=>[`## ${m.role==='user'?'User':'Assistant'}`,m.content,''])].join('\n');const blob=new Blob([content],{type:format==='json'?'application/json':'text/markdown'});const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=`web-chat-${s.id}.${format==='json'?'json':'md'}`;a.click();URL.revokeObjectURL(url)}

function draftKey(id:number|null){return`subapis.webChat.draft.${authStore.user?.id||'anonymous'}.${id??'new'}`}
function groupName(id:number){return options.value.groups.find(g=>g.id===id)?.name||`#${id}`}
function hasUsage(m:WebChatMessage){return m.input_tokens+m.output_tokens+m.cache_read_tokens+m.cache_creation_tokens>0}
function formatTokens(value:number){return new Intl.NumberFormat(undefined,{notation:'compact',maximumFractionDigits:1}).format(value||0)}
function estimatedCost(m:WebChatMessage){const p=selectedModelOption.value?.pricing;if(!p)return null;const rate=selectedGroup.value?.rate_multiplier??1;if(p.billing_mode==='per_request')return(p.per_request_price??0)*rate;return((m.input_tokens*(p.input_price??0))+(m.output_tokens*(p.output_price??0))+(m.cache_read_tokens*(p.cache_read_price??0))+(m.cache_creation_tokens*(p.cache_write_price??0)))*rate}
function shortRequestID(id:string){return id.length>16?`${id.slice(0,8)}…${id.slice(-6)}`:id}
function formatMessageTime(value:string){const d=new Date(value);return Number.isNaN(d.getTime())?'':new Intl.DateTimeFormat(undefined,{hour:'2-digit',minute:'2-digit'}).format(d)}
async function scrollToBottom(){await nextTick();if(messageListRef.value)messageListRef.value.scrollTop=messageListRef.value.scrollHeight}
function startPanelResize(panel:'session'|'context',event:PointerEvent){if(window.innerWidth<1280)return;event.preventDefault();resizingPanel.value=panel;document.body.classList.add('web-chat-resizing');window.addEventListener('pointermove',handlePanelResize);window.addEventListener('pointerup',stopPanelResize,{once:true});handlePanelResize(event)}
function handlePanelResize(event:PointerEvent){const rect=webChatShellRef.value?.getBoundingClientRect();if(!rect||!resizingPanel.value)return;if(resizingPanel.value==='session')sessionPanelWidth.value=clamp(event.clientX-rect.left,208,420);else contextPanelWidth.value=clamp(rect.right-event.clientX,232,440)}
function stopPanelResize(){if(resizingPanel.value){localStorage.setItem(`subapis.webChat.${resizingPanel.value}PanelWidth`,String(resizingPanel.value==='session'?sessionPanelWidth.value:contextPanelWidth.value))}resizingPanel.value=null;document.body.classList.remove('web-chat-resizing');window.removeEventListener('pointermove',handlePanelResize)}
function resetPanelWidth(panel:'session'|'context'){if(panel==='session')sessionPanelWidth.value=256;else contextPanelWidth.value=288;localStorage.removeItem(`subapis.webChat.${panel}PanelWidth`)}
function restorePanelWidths(){sessionPanelWidth.value=clamp(Number(localStorage.getItem('subapis.webChat.sessionPanelWidth'))||256,208,420);contextPanelWidth.value=clamp(Number(localStorage.getItem('subapis.webChat.contextPanelWidth'))||288,232,440)}
function clamp(value:number,min:number,max:number){return Math.min(max,Math.max(min,Math.round(value)))}
</script>

<style scoped>
.web-chat-shell{display:grid;grid-template-columns:var(--session-width,16rem) .35rem minmax(0,1fr) .35rem var(--context-width,18rem);gap:.45rem;height:calc(100vh - 7.5rem);min-height:42rem}.session-panel,.chat-panel,.context-card{border:1px solid rgb(229 231 235);background:rgba(255,255,255,.9);box-shadow:0 18px 50px rgba(15,23,42,.06)}.dark .session-panel,.dark .chat-panel,.dark .context-card{border-color:#334155;background:rgba(15,23,42,.9)}.resize-handle{position:relative;cursor:col-resize;touch-action:none}.resize-handle:before{position:absolute;inset:1rem 50%;width:2px;transform:translateX(-50%);border-radius:999px;background:rgba(148,163,184,.35);content:""}.resize-handle:hover:before{width:4px;background:#14b8a6}:global(body.web-chat-resizing){cursor:col-resize;user-select:none}
.session-panel{display:flex;flex-direction:column;min-height:0;border-radius:1.5rem;padding:1rem}.eyebrow,.context-label,.pricing-title{font-size:.68rem;font-weight:850;letter-spacing:.15em;text-transform:uppercase;color:#0d9488}.new-chat{display:flex;align-items:center;justify-content:center;gap:.4rem;margin-top:1rem;border-radius:1rem;background:#0d9488;padding:.7rem;color:white;font-size:.85rem;font-weight:750}.new-chat:disabled{opacity:.5}.search-box{display:flex;align-items:center;gap:.4rem;margin-top:.8rem;border:1px solid #d1d5db;border-radius:.8rem;padding:.45rem .6rem}.search-box input{min-width:0;width:100%;background:transparent;outline:none;font-size:.8rem}.session-list{margin-top:.7rem;min-height:0;overflow-y:auto}.session-item{display:flex;align-items:center;gap:.25rem;border:1px solid transparent;border-radius:.9rem;padding:.65rem;margin-bottom:.25rem}.session-item:hover,.session-item.active{border-color:rgba(20,184,166,.3);background:rgba(20,184,166,.08)}.session-actions{display:none;gap:.25rem;color:#64748b}.session-item:hover .session-actions{display:flex}.pin-dot{color:#0d9488;font-size:.5rem}.empty-small{padding:2rem .5rem;text-align:center;color:#94a3b8;font-size:.8rem}
.project-nav{margin-top:.8rem;border-bottom:1px solid rgba(148,163,184,.2);padding-bottom:.6rem}.project-add{border-radius:.5rem;padding:.1rem .4rem;color:#0d9488;font-weight:900}.project-item{display:flex;width:100%;align-items:center;gap:.45rem;border-radius:.65rem;padding:.4rem .5rem;text-align:left;font-size:.75rem;color:#64748b}.project-item:hover,.project-item.active{background:rgba(20,184,166,.08);color:#0f766e}.project-item i{width:.5rem;height:.5rem;border-radius:99px}.project-item b{min-width:0;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.project-item span{font-size:.65rem}.project-item em{font-style:normal;opacity:.5}
.chat-panel{display:flex;min-height:0;flex-direction:column;overflow:hidden;border-radius:1.6rem}.chat-header{display:flex;align-items:center;gap:.6rem;border-bottom:1px solid rgba(148,163,184,.25);padding:.9rem 1rem}.header-actions{display:flex;gap:.3rem}.icon-button{display:inline-flex;align-items:center;gap:.25rem;border:1px solid rgba(148,163,184,.3);border-radius:.65rem;padding:.42rem;font-size:.65rem}.icon-button.danger{color:#ef4444}.message-list{flex:1;overflow-y:auto;padding:1.2rem}.empty-state{display:flex;height:100%;min-height:18rem;align-items:center;justify-content:center;flex-direction:column;text-align:center;gap:.6rem;color:#64748b}.empty-state h2{font-size:1.05rem;font-weight:800;color:#1f2937}.dark .empty-state h2{color:#f1f5f9}.quick-prompts{display:flex;flex-wrap:wrap;justify-content:center;gap:.5rem;margin-top:.6rem}.quick-prompts button{border:1px solid rgba(20,184,166,.25);border-radius:999px;padding:.45rem .7rem;font-size:.75rem;color:#0f766e;background:rgba(20,184,166,.06)}
.message-row{display:flex;margin-bottom:1rem}.message-row.user{justify-content:flex-end}.message-stack{display:flex;max-width:min(52rem,92%);flex-direction:column;gap:.3rem}.message-row.user .message-stack{align-items:flex-end}.message-bubble{border-radius:1.1rem;padding:.65rem .9rem}.message-bubble.user{background:linear-gradient(135deg,#0f766e,#0891b2);color:white}.message-bubble.assistant{border:1px solid rgba(148,163,184,.25);background:rgba(248,250,252,.9)}.dark .message-bubble.assistant{background:#0f172a;color:#e2e8f0}.message-error{margin-top:.5rem;color:#ef4444;font-size:.72rem}.message-footer,.message-actions{display:flex;flex-wrap:wrap;gap:.5rem;color:#94a3b8;font-size:.65rem}.message-actions{opacity:0;transition:opacity .15s}.message-stack:hover .message-actions{opacity:1}.message-actions button{display:flex;align-items:center;gap:.2rem}.typing{font-size:.85rem;color:#64748b}
.version-switch{display:inline-flex;align-items:center;gap:.28rem;border-radius:999px;background:rgba(20,184,166,.1);padding:.1rem .35rem;color:#0f766e}.version-switch button:disabled{opacity:.3}.version-switch small{border-left:1px solid rgba(20,184,166,.25);padding-left:.3rem}.context-panel{min-height:0;overflow-y:auto}.context-card{border-radius:1.5rem;padding:1rem}.context-card label{display:flex;justify-content:space-between;margin-top:.8rem;margin-bottom:.3rem;font-size:.72rem;font-weight:700;color:#64748b}.input{width:100%;border:1px solid #d1d5db;border-radius:.7rem;background:transparent;padding:.5rem;font-size:.8rem}.advanced-toggle{display:flex;width:100%;align-items:center;justify-content:space-between;margin-top:1rem;border-top:1px solid rgba(148,163,184,.2);padding-top:.8rem;font-size:.8rem;font-weight:750}.save-settings{width:100%;margin-top:.8rem;border-radius:.7rem;background:#0d9488;padding:.5rem;color:white;font-size:.8rem;font-weight:700}.save-settings:disabled{opacity:.45}.pricing-card{margin-top:1rem;border-radius:1rem;background:rgba(20,184,166,.07);padding:.75rem}.pricing-grid{display:grid;grid-template-columns:1fr 1fr;gap:.45rem;margin-top:.5rem}.pricing-grid div{display:flex;flex-direction:column;font-size:.68rem;color:#64748b}.pricing-grid strong{font-size:.75rem;color:#0f766e}
@media(max-width:1279px){.web-chat-shell{grid-template-columns:minmax(0,1fr) 17rem;gap:.75rem}.resize-handle{display:none}.session-panel{position:fixed;z-index:50;left:1rem;top:5rem;bottom:1rem;width:min(20rem,calc(100vw - 2rem));transform:translateX(calc(-100% - 2rem));transition:transform .2s}.session-panel-open{transform:translateX(0)}}@media(max-width:767px){.web-chat-shell{display:block;height:calc(100vh - 5.5rem);min-height:32rem}.context-panel{display:none}.message-list{padding:.8rem}.message-stack{max-width:96%}.header-actions .icon-button span{display:none}}@media(hover:none){.message-actions{opacity:1}}

.session-toggle,.context-toggle,.panel-scrim{display:none}
@media(max-width:1535px){
  .web-chat-shell{grid-template-columns:minmax(0,1fr) 17rem;gap:.75rem}
  .resize-handle{display:none}
  .session-toggle{display:inline-flex}
  .session-panel{position:fixed;z-index:60;left:1rem;top:5rem;bottom:1rem;width:min(20rem,calc(100vw - 2rem));transform:translateX(calc(-100% - 2rem));transition:transform .2s}
  .session-panel-open{transform:translateX(0)}
  .panel-scrim{position:fixed;inset:0;z-index:55;display:block;background:rgba(15,23,42,.34);backdrop-filter:blur(2px)}
}
@media(max-width:767px){
  .web-chat-shell{margin-top:4rem}
  .context-toggle{display:inline-flex}
  .context-panel{position:fixed;z-index:60;top:0;right:0;bottom:0;display:block;width:min(22rem,calc(100vw - 1.25rem));padding:.75rem;overflow-y:auto;transform:translateX(105%);transition:transform .2s}
  .context-panel-open{transform:translateX(0)}
  .context-card{min-height:100%;border-radius:1.25rem}
}
</style>
