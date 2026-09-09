<template>
  <WebChatWorkbench :section="workspaceSection" :busy="sending || creatingSession || agent.creating.value" :projects-enabled="options.projects_enabled" :files-enabled="options.files_enabled" :artifacts-enabled="Boolean(options.task_limits||options.tasks_enabled)" :templates-enabled="options.templates_enabled" @home="goHome" @navigate="navigateWorkspace" @templates="templateDialogOpen=true">
    <div class="web-chat-shell">
      <aside class="session-panel" :class="{ 'session-panel-open': sessionsOpen }">
        <div class="sidebar-top"><button class="new-chat" :disabled="sending" @click="goHome"><Icon name="plus" size="sm" />{{ t('webChat.newChat') }}</button><button class="icon-button session-toggle" :aria-label="t('common.close')" @click="sessionsOpen=false"><Icon name="x" size="sm" /></button></div>
        <div class="search-box"><Icon name="search" size="sm" /><input v-model="sessionQuery" :aria-label="t('webChat.searchSessions')" :placeholder="t('webChat.searchSessions')" /></div>
        <p class="section-label">{{ t('webChat.sessions') }}</p>
        <div class="session-list">
          <article v-for="session in displayedSessions" :key="session.id" class="session-item" :class="{active:session.id===activeSessionId}">
            <button class="session-select" :disabled="sending" @click="selectSession(session)"><span v-if="session.pinned_at">◆</span><span>{{ session.title || sessionModelLabel(session) }}</span></button>
            <div class="session-actions"><button :aria-label="t('webChat.pin')" :disabled="sending" @click="togglePin(session)">◆</button><button :aria-label="t('webChat.rename')" :disabled="sending" @click="renameSession(session)"><Icon name="edit" size="xs" /></button></div>
          </article>
          <p v-if="!displayedSessions.length&&!loading" class="empty-small">{{ t('webChat.noSessions') }}</p>
        </div>
        <div v-if="options.projects_enabled" class="project-nav">
          <div class="section-heading"><span>{{ t('webChat.projects') }}</span><button class="icon-button" :aria-label="t('webChat.newProject')" :disabled="sending" @click="openProjectEditor(null)"><Icon name="plus" size="xs" /></button></div>
          <button class="project-item" :class="{active:projectFilter==='all'}" :disabled="sending" @click="projectFilter='all'">{{ t('webChat.allChats') }}</button>
          <div v-for="project in projects" :key="project.id" class="project-row">
            <button class="project-item" :class="{active:projectFilter===project.id}" :disabled="sending" @click="chooseProject(project)"><Icon name="folder" size="sm" /><span>{{ project.name }}</span></button>
            <button class="icon-button" :aria-label="t('workspace.projectSettings')" :disabled="sending" @click="openProjectEditor(project)"><Icon name="more" size="sm" /></button>
          </div>
        </div>
        <button v-if="options.templates_enabled" class="assistant-entry" :disabled="sending" @click="templateDialogOpen=true"><Icon name="sparkles" size="sm" />{{ t('workspace.assistants') }}</button>
        <RouterLink class="sidebar-footer" to="/dashboard"><Icon name="cog" size="sm" />{{ t('workspace.console') }}</RouterLink>
      </aside>
      <main id="web-agent-main" class="chat-panel" tabindex="-1">
        <header class="chat-header">
          <button class="icon-button session-toggle" :aria-label="t('webChat.openSessions')" @click="sessionsOpen=true"><Icon name="menu" size="sm" /></button>
          <div class="chat-heading"><h1>{{ workspaceSection==='chat' ? activeSession?.title || selectedProject?.name || t('workspace.home') : workspaceSection==='files' ? t('workspace.files') : t('webChat.projects') }}</h1><p v-if="activeSession?.project_name">{{ activeSession.project_name }}</p></div>
          <button v-if="workspaceSection==='chat'" class="model-trigger" :disabled="sending" @click="contextOpen=true"><Icon name="sparkles" size="sm" /><span>{{ selectedModelOption?.display_name || selectedModel || t('webChat.selectModel') }}</span><Icon name="chevronDown" size="xs" /></button>
          <button v-if="workspaceSection==='chat'&&agent.artifacts.value.length" class="new-chat" @click="inspectArtifact(agent.artifacts.value[0]!.id)">{{ t('webAgent.files') }} · {{ agent.artifacts.value.length }}</button>
          <details v-if="activeSession&&workspaceSection==='chat'" class="header-menu"><summary class="icon-button" :aria-label="t('workspace.actions')"><Icon name="more" size="sm" /></summary><div>
            <button @click="exportConversation('markdown')">{{ t('webChat.exportMarkdown') }}</button>
            <button @click="exportConversation('json')">{{ t('webChat.exportJson') }}</button>
            <button class="danger" :disabled="sending" @click="removeCurrentSession">{{ t('common.delete') }}</button>
          </div></details>
        </header>
        <div v-if="operationError" role="alert" class="workspace-error">{{ operationError }} <button @click="operationError=''">{{ t('common.close') }}</button></div>
        <div v-if="workspaceSection==='chat'&&enabled" class="task-modebar" :aria-label="t('webAgent.mode')">
          <button v-for="mode in taskModes" :key="mode" :aria-pressed="taskMode===mode" :disabled="sending||agent.creating.value" @click="setTaskMode(mode)">{{ t(`webAgent.${mode}`) }}</button>
        </div>
        <div v-if="taskMode!=='chat'&&workspaceSection==='chat'" class="task-context">
          <p v-if="!options.tasks_enabled">{{ t('webAgent.unavailable') }} <button @click="refreshTaskOptions">{{ t('webAgent.refresh') }}</button></p>
          <p v-else>{{ t('webAgent.budget',{minutes:(options.task_limits?.deadline_seconds||600)/60}) }}</p>
          <details v-if="options.task_limits"><summary>{{ t('workspace.usageDetails') }}</summary>{{ t('webAgent.budgetDetails',{input:options.task_limits.max_input_bytes/1024,output:activeSession?.max_output_tokens||8192,file:options.task_limits.max_artifact_bytes/1024/1024}) }}</details>
          <p v-if="sourceArtifact">{{ t('webAgent.source',{version:sourceArtifact.version}) }} · {{ sourceArtifact.title }} <button @click="sourceArtifact=null">{{ t('webAgent.clearSource') }}</button></p>
          <p v-if="agent.error.value" role="alert">{{ agent.error.value }}</p>
          <p v-if="agent.pending.value">{{ t('webAgent.pending') }} <button :disabled="agent.creating.value" @click="retryTaskSubmission">{{ t('webAgent.retrySubmission') }}</button></p>
        </div>
        <WebAgentArtifactLibrary v-if="workspaceSection==='files'" ref="artifactLibrary" :enabled="Boolean(options.task_limits||options.tasks_enabled)" :user-id="authStore.user?.id" :sessions="sessions" @open="inspectArtifact" @conversation="openArtifactConversation" @loaded="libraryFiles=$event">
          <template v-if="options.files_enabled&&options.projects_enabled" #references><details class="reference-library"><summary>{{ t('webAgent.referenceLibrary') }}</summary><p>{{ t('workspace.filesHint') }}</p><button v-for="project in projects" :key="project.id" @click="openKnowledgeLibrary(project)"><Icon name="folder" size="sm" />{{ project.name }}</button><p v-if="!projects.length">{{ t('workspace.noProjects') }}</p></details></template>
        </WebAgentArtifactLibrary>
        <section v-else-if="workspaceSection==='projects'" class="projects-workspace">
          <p>{{ t('workspace.projectHint') }}</p>
          <button class="new-chat" :disabled="sending" @click="openProjectEditor(null)"><Icon name="plus" size="sm" />{{ t('workspace.createProject') }}</button>
          <div v-if="projects.length" class="project-grid"><article v-for="project in projects" :key="project.id">
            <Icon name="folder" size="md" /><h2>{{ project.name }}</h2><p>{{ project.description }}</p>
            <div><button :disabled="sending" @click="chooseProject(project)">{{ t('workspace.startProject') }}</button><button v-if="options.files_enabled" @click="openKnowledgeLibrary(project)">{{ t('workspace.openFiles') }}</button></div>
          </article></div>
          <div v-else class="empty-state"><h2>{{ t('workspace.noProjects') }}</h2><p>{{ t('workspace.noProjectsHint') }}</p></div>
        </section>
        <div v-else-if="showHome&&taskMode==='chat'" class="home-scroll"><WebChatHome :name="authStore.user?.username" :sessions="displayedSessions" :disabled="sending" @select="selectSession" @shortcut="chooseShortcut">
          <template #composer><WebChatComposer v-model="draft" :disabled="!canCompose" :can-send="canSend&&Boolean(draft.trim())" :sending="sending" :files-enabled="options.files_enabled" :templates-enabled="options.templates_enabled" :template-name="activeTemplateName" :documents="pendingDocuments" :failed-attachments="failedAttachments" :attachment-state="attachmentState" @submit="send" @stop="stopGeneration" @open-template="templateDialogOpen=true" @files="uploadTemporaryDocuments" @remove-document="removePendingDocument" @retry-attachment="retryFailedAttachment" @remove-failed-attachment="removeFailedAttachment"/></template>
        </WebChatHome></div>
        <template v-else>
          <p v-if="taskMode==='chat'" class="chat-capability-hint">{{ t('webAgent.chatHint') }}</p>
          <WebAgentTaskFeed v-if="taskMode!=='chat'" :tasks="agent.tasks.value" :events="agent.events.value" :has-more="Boolean(agent.nextBefore.value)" :loading="agent.loading.value" @open="inspectArtifact" @cancel="cancelFileTask" @details="loadTaskDetails" @older="agent.older" />
          <section v-else ref="messageListRef" class="message-list" @scroll="onMessageScroll">
            <div v-if="loading || messagesLoading" class="empty-state" role="status"><Icon name="refresh" size="lg" class="animate-spin" /><h2>{{ t('common.loading') }}</h2></div>
            <div v-else-if="!enabled" class="empty-state"><Icon name="lock" size="lg" /><h2>{{ t('webChat.disabledTitle') }}</h2><p>{{ t('webChat.disabledDescription') }}</p><button v-if="operationError" @click="loadInitial">{{ t('workspace.retry') }}</button></div>
            <div v-else-if="!hasUsableModel" class="empty-state"><h2>{{ t('webChat.noGroupsTitle') }}</h2><p>{{ t('webChat.noGroupsDescription') }}</p></div>
            <div v-else-if="messages.length===0" class="empty-state"><h2>{{ t('workspace.startTitle') }}</h2></div>
            <template v-for="message in messages" :key="message.id">
              <article class="message-row" :class="message.role">
                <div class="message-stack">
                  <div v-if="message.role==='assistant'" class="assistant-name"><span>S</span> SubAPIs</div>
                  <div class="message-bubble" :class="message.role"><WebChatMessageContent :content="message.content" :markdown="message.role==='assistant'" /><p v-if="message.status==='error'||message.status==='partial'" class="message-error">{{ message.error_message || t('webChat.streamError') }}</p></div>
                  <WebChatSources :sources="message.sources || []" />
                  <div class="message-actions">
                    <button @click="copyText(message.content)"><Icon name="copy" size="xs" />{{ t('webChat.copy') }}</button>
                    <button @click="quoteMessage(message)"><Icon name="chatBubble" size="xs" />{{ t('webChat.quote') }}</button>
                    <button v-if="message.role==='user'" :disabled="sending" @click="reviseMessage(message)"><Icon name="edit" size="xs" />{{ t('webChat.editResend') }}</button>
                    <button v-else :disabled="sending" @click="regenerateMessage(message)"><Icon name="refresh" size="xs" />{{ message.status==='error'?t('webChat.retry'):t('webChat.regenerate') }}</button>
                    <details v-if="message.role==='assistant'&&hasUsage(message)" class="usage-details"><summary>{{ t('workspace.usageDetails') }}</summary><div><span>↑ {{ formatTokens(message.input_tokens) }} · ↓ {{ formatTokens(message.output_tokens) }}</span><span>Cache {{ formatTokens(message.cache_read_tokens) }}</span><code v-if="message.request_id">{{ message.request_id }}</code></div></details>
                  </div>
                  <div v-if="options.history_enabled&&message.version_count>1" class="version-switch"><button :disabled="message.version_index<=1||sending" @click="switchVersion(message,-1)" :aria-label="t('common.previous')">‹</button><span>{{ message.version_index }} / {{ message.version_count }} · {{ versionReason(message.version_reason) }}</span><button :disabled="message.version_index>=message.version_count||sending" @click="switchVersion(message,1)" :aria-label="t('common.next')">›</button></div>
                </div>
              </article>
            </template>
            <article v-if="sending" class="message-row assistant" role="status"><div class="message-stack"><div class="assistant-name"><span>S</span> SubAPIs</div><WebChatMessageContent v-if="streamingText" :content="streamingText" markdown /><p v-else class="typing">{{ t('workspace.waiting') }}…</p></div></article>
          </section>
          <div v-if="canCompose" class="composer-dock"><WebChatComposer v-model="draft" :disabled="!canCompose||agent.creating.value" :can-send="canSend&&Boolean(draft.trim())" :sending="sending" :files-enabled="options.files_enabled" :templates-enabled="options.templates_enabled" :template-name="activeTemplateName" :documents="pendingDocuments" :failed-attachments="failedAttachments" :attachment-state="attachmentState" @submit="send" @stop="stopGeneration" @open-template="templateDialogOpen=true" @files="uploadTemporaryDocuments" @remove-document="removePendingDocument" @retry-attachment="retryFailedAttachment" @remove-failed-attachment="removeFailedAttachment"/></div>
        </template>
      </main>
      <WebAgentArtifactPane v-if="selectedArtifact&&workspaceSection!=='projects'" :artifact="selectedArtifact" :files="workspaceSection==='files'?libraryFiles:agent.artifacts.value" :can-revise="Boolean(options.tasks_enabled)&&!sending&&!agent.creating.value" @close="selectedArtifact=null" @select="inspectArtifact" @revise="reviseArtifact" @deleted="artifactDeleted" />
    </div>
    <BaseDialog :show="contextOpen" :title="t('workspace.modelSettings')" @close="contextOpen=false">
      <div class="context-card">
        <label>{{ t('webChat.group') }}<select v-model.number="selectedGroupId" class="input" :disabled="sending"><option v-for="group in options.groups" :key="group.id" :value="group.id">{{ groupOptionLabel(group) }}</option></select></label>
        <label>{{ t('webChat.model') }}<select v-model="selectedModel" class="input" :disabled="sending"><option v-for="model in selectedGroupModels" :key="model.name" :value="model.name">{{ model.display_name || modelDisplayName(model.name,selectedGroup?.platform) }}</option></select></label>
        <label v-if="options.projects_enabled&&activeSession">{{ t('webChat.project') }}<select :value="activeSession.project_id||''" class="input" :disabled="sending" @change="moveActiveSession"><option value="">{{ t('webChat.uncategorized') }}</option><option v-for="project in projects" :key="project.id" :value="project.id">{{ project.name }}</option></select></label>
        <label v-if="options.files_enabled&&activeSession" class="knowledge-toggle"><span>{{ t('webChat.useProjectKnowledge') }}</span><input type="checkbox" :checked="activeSession.knowledge_enabled" :disabled="sending" @change="toggleKnowledge" /></label>
        <details class="advanced-settings"><summary>{{ t('webChat.advanced') }}</summary><label>{{ t('webChat.systemPrompt') }}<textarea v-model="systemPrompt" rows="5" maxlength="8000" class="input" :disabled="sending" /></label><label>{{ t('webChat.temperature') }}<input v-model="temperatureInput" type="number" class="input" min="0" max="2" step=".1" :disabled="sending" /></label><label>{{ t('webChat.maxOutputTokens') }}<input v-model.number="maxOutputTokens" type="number" class="input" min="1" max="32768" :disabled="sending" /></label><button class="new-chat" :disabled="!activeSession||savingSettings||sending" @click="saveAdvancedSettings">{{ t('common.save') }}</button></details>
        <div class="pricing-card"><h3>{{ t('webChat.priceHint') }}</h3><div v-for="item in pricingItems" :key="item.label"><span>{{ item.label }}</span><strong>{{ item.value }}</strong></div></div>
      </div>
    </BaseDialog>
    <button v-if="sessionsOpen" class="panel-scrim" :aria-label="t('common.close')" @click="sessionsOpen=false" />
    <WebChatProjectDialog :show="projectDialogOpen" :project="editingProject" :groups="options.groups" :templates="templates" @close="projectDialogOpen=false" @saved="onProjectSaved" @deleted="onProjectDeleted" />
    <WebChatTemplateDialog :show="templateDialogOpen" :templates="localizedTemplates" @close="templateDialogOpen=false" @apply="applyTemplate" @changed="loadTemplates" />
    <WebChatKnowledgeLibrary :show="knowledgeLibraryOpen" :project="knowledgeProject" :limits="options.file_limits" @close="knowledgeLibraryOpen=false" />
  </WebChatWorkbench>
</template>
<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import WebChatWorkbench from '@/components/web-chat/WebChatWorkbench.vue'
import WebChatHome from '@/components/web-chat/WebChatHome.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useRoute, useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'
import WebChatMessageContent from '@/components/web-chat/WebChatMessageContent.vue'
import WebChatProjectDialog from '@/components/web-chat/WebChatProjectDialog.vue'
import WebChatTemplateDialog from '@/components/web-chat/WebChatTemplateDialog.vue'
import WebChatKnowledgeLibrary from '@/components/web-chat/WebChatKnowledgeLibrary.vue'
import WebChatSources from '@/components/web-chat/WebChatSources.vue'
import WebChatComposer from '@/components/web-chat/WebChatComposer.vue'
import WebAgentTaskFeed from '@/components/web-chat/WebAgentTaskFeed.vue'
import WebAgentArtifactPane from '@/components/web-chat/WebAgentArtifactPane.vue'
import WebAgentArtifactLibrary from '@/components/web-chat/WebAgentArtifactLibrary.vue'
import { useWebAgentTasks } from '@/composables/useWebAgentTasks'
import { getArtifact, type WebAgentArtifact, type WebAgentTask } from '@/api/webAgent'
import webChatAPI, { type WebChatMessage, type WebChatOptions, type WebChatProject, type WebChatSession, type WebChatSource, type WebChatStreamHandlers, type WebChatTemplate } from '@/api/webChat'
import { useAuthStore } from '@/stores/auth'
import { extractApiErrorMessage } from '@/utils/apiError'
import { platformLabel } from '@/utils/platformColors'
import { formatScaled } from '@/utils/pricing'
import { selectLocalizedWebChatTemplates } from '@/utils/webChatTemplates'
import { useWebChatDocuments } from '@/composables/useWebChatDocuments'

const { t, locale } = useI18n(); const authStore = useAuthStore()
const route=useRoute(), router=useRouter()
const workspaceSection=ref<'chat'|'projects'|'files'>('chat'), operationError=ref(''), messagesLoading=ref(false)
let selectionVersion=0
let pendingSession:Promise<WebChatSession|null>|null=null
const followOutput=ref(true)
const showHome=computed(()=>!activeSessionId.value&&enabled.value&&!loading.value&&hasUsableModel.value)
function onMessageScroll(){const el=messageListRef.value;if(el)followOutput.value=el.scrollHeight-el.scrollTop-el.clientHeight<100}
async function goHome(){if(sending.value||agent.creating.value)return;artifactSelection++;workspaceSection.value='chat';taskMode.value='chat';await startDraftSession()}
async function chooseProject(project:WebChatProject){if(sending.value||agent.creating.value)return;projectFilter.value=project.id;await goHome()}
function chooseShortcut(kind:string){setTaskMode(kind==='analysis'?'spreadsheet':kind as TaskMode);void nextTick(()=>document.querySelector<HTMLTextAreaElement>('.composer-input')?.focus())}
const loading=ref(true), creatingSession=ref(false), sending=ref(false), savingSettings=ref(false), enabled=ref(false), sessionsOpen=ref(false), contextOpen=ref(false)
const projectDialogOpen=ref(false),templateDialogOpen=ref(false),editingProject=ref<WebChatProject|null>(null),projectFilter=ref<'all'|number|null>('all')
const knowledgeLibraryOpen=ref(false),knowledgeProject=ref<WebChatProject|null>(null),streamingSources=ref<WebChatSource[]>([])
const options=ref<WebChatOptions>({enabled:false,groups:[],projects_enabled:false,templates_enabled:false,history_enabled:false,files_enabled:false,file_limits:{max_file_bytes:20*1024*1024,max_files_per_project:50,max_bytes_per_user:500*1024*1024}}), sessions=ref<WebChatSession[]>([]), messages=ref<WebChatMessage[]>([]),projects=ref<WebChatProject[]>([]),templates=ref<WebChatTemplate[]>([])
const activeTemplateId=ref<number|null>(null)
const activeSessionId=ref<number|null>(null), selectedGroupId=ref<number|null>(null), selectedModel=ref(''), draft=ref(''), streamingText=ref(''), sessionQuery=ref('')
const taskModes=['chat','slides','spreadsheet','document'] as const
type TaskMode=typeof taskModes[number]
const taskMode=ref<TaskMode>('chat'),selectedArtifact=ref<WebAgentArtifact|null>(null),sourceArtifact=ref<WebAgentArtifact|null>(null)
const artifactLibrary=ref<InstanceType<typeof WebAgentArtifactLibrary>>(),libraryFiles=ref<WebAgentArtifact[]>([])
const agent=useWebAgentTasks(activeSessionId,computed(()=>authStore.user?.id),computed(()=>Boolean(options.value.task_limits||options.value.tasks_enabled)))
let artifactSelection=0
const systemPrompt=ref(''), temperatureInput=ref(''), maxOutputTokens=ref(8192), messageListRef=ref<HTMLElement|null>(null)
const abortController=ref<AbortController|null>(null)
const {pendingDocuments,failedAttachments,attachmentState,uploadTemporaryDocuments,retryFailedAttachment,removePendingDocument,removeFailedAttachment,clearPendingDocuments,discardPendingDocuments}=useWebChatDocuments(async()=>activeSession.value||await createSessionForCurrentSelection(),key=>t(key))

const activeSession=computed(()=>sessions.value.find(item=>item.id===activeSessionId.value)||null)
const selectedGroup=computed(()=>options.value.groups.find(group=>group.id===selectedGroupId.value)||null)
const selectedGroupModels=computed(()=>selectedGroup.value?.models||[])
const selectedModelOption=computed(()=>selectedGroupModels.value.find(model=>model.name===selectedModel.value)||null)
const hasUsableModel=computed(()=>options.value.groups.some(group=>group.models.length>0))
const displayedSessions=computed(()=>sessions.value.filter(s=>(projectFilter.value==='all'||(s.project_id??null)===projectFilter.value)&&[s.title,s.model,s.group_name||''].some(value=>value.toLowerCase().includes(sessionQuery.value.trim().toLowerCase()))))
const selectedProject=computed(()=>typeof projectFilter.value==='number'?projects.value.find(p=>p.id===projectFilter.value)||null:null)
const activeTemplateName=computed(()=>templates.value.find(x=>x.id===activeTemplateId.value)?.name||'')
const localizedTemplates=computed(()=>selectLocalizedWebChatTemplates(templates.value,locale.value))
const canCompose=computed(()=>enabled.value&&hasUsableModel.value&&!creatingSession.value)
const canSend=computed(()=>canCompose.value&&!messagesLoading.value&&!sending.value&&!agent.creating.value&&(taskMode.value==='chat'||(Boolean(options.value.tasks_enabled)&&!attachmentState.value&&!failedAttachments.value.length&&pendingDocuments.value.every(d=>d.status==='ready')))&&Boolean(selectedGroup.value&&selectedModel.value)&&draft.value.length<=20000)
const pricingItems=computed(()=>{const p=selectedModelOption.value?.pricing;if(!p)return[];if(p.billing_mode==='per_request')return[{label:t('webChat.perRequest'),value:formatScaled(p.per_request_price??null,1)}];return[
  {label:t('webChat.inputPrice'),value:formatScaled(p.input_price??null,1_000_000)},{label:t('webChat.outputPrice'),value:formatScaled(p.output_price??null,1_000_000)},
  {label:t('webChat.cacheWritePrice'),value:formatScaled(p.cache_write_price??null,1_000_000)},{label:t('webChat.cacheReadPrice'),value:formatScaled(p.cache_read_price??null,1_000_000)}].filter(i=>i.value!=='-')})

function modelDisplayName(model: string, platform?: string): string {
  const name = (model || '').trim()
  if ((platform || '').trim().toLowerCase() !== 'kiro') return name
  for (const suffix of ['-kiro', '_kiro', ' (kiro)', ' [kiro]', ' - kiro']) {
    if (name.toLowerCase().endsWith(suffix) && name.length > suffix.length) {
      return name.slice(0, -suffix.length).trim()
    }
  }
  return name
}

function groupOptionLabel(group: WebChatOptions['groups'][number]): string {
  const name = (group.name || '').trim()
  // Claude-AWS is already an explicit user-facing channel name; appending
  // "· Kiro" only exposes the internal provider classification twice.
  if (group.platform.trim().toLowerCase() === 'kiro' && /(?:aws|kiro)/i.test(name)) return name
  return `${name} · ${platformLabel(group.platform)}`
}

function sessionModelLabel(session: WebChatSession): string {
  const platform = session.platform || options.value.groups.find(group => group.id === session.group_id)?.platform
  const option = options.value.groups.find(group => group.id === session.group_id)?.models.find(model => model.name === session.model)
  return option?.display_name || modelDisplayName(session.model, platform)
}

watch(selectedGroupId,()=>{if(!selectedGroupModels.value.some(m=>m.name===selectedModel.value))selectedModel.value=selectedGroupModels.value[0]?.name||''})
watch(draft,value=>localStorage.setItem(draftKey(activeSessionId.value),value))
watch(activeSessionId,id=>{draft.value=localStorage.getItem(draftKey(id))||''})
watch(activeSessionId,()=>{artifactSelection++;selectedArtifact.value=null;sourceArtifact.value=null})
watch(projectFilter,()=>{if(!activeSession.value)startDraftSession()})
onMounted(()=>{void loadInitial()})
onBeforeUnmount(()=>{selectionVersion++;artifactSelection++;abortController.value?.abort();if(agent.pending.value)clearPendingDocuments();else void discardPendingDocuments()})

async function loadInitial(){
 loading.value=true
 const intent=selectionVersion
 try{
  const[opts,list]=await Promise.all([webChatAPI.getOptions(),webChatAPI.listSessions()])
  options.value=opts;enabled.value=opts.enabled;sessions.value=list
  selectedGroupId.value=opts.default_group_id??opts.groups[0]?.id??null;selectedModel.value=opts.default_model||opts.groups[0]?.models[0]?.name||''
  await Promise.all([opts.projects_enabled?loadProjects():Promise.resolve(),opts.templates_enabled?loadTemplates():Promise.resolve()])
  const requested=Number(route.query.session)
  if(intent===selectionVersion&&Number.isSafeInteger(requested)&&requested>0){
   const target=list.find(s=>s.id===requested)||await webChatAPI.getSession(requested)
   if(intent!==selectionVersion)return
   if(!sessions.value.some(s=>s.id===target.id))sessions.value.push(target)
   await selectSession(target)
  }
 }catch(e){if(intent===selectionVersion)operationError.value=extractApiErrorMessage(e,t('workspace.loadFailed'))}
 finally{loading.value=false}
}
async function refreshSessions(){sessions.value=await webChatAPI.listSessions().catch(()=>sessions.value)}
async function loadProjects(){if(!options.value.projects_enabled)return;projects.value=await webChatAPI.listProjects().catch(()=>projects.value)}
async function loadTemplates(){if(!options.value.templates_enabled)return;templates.value=await webChatAPI.listTemplates().catch(()=>templates.value)}
function openProjectEditor(project:WebChatProject|null){editingProject.value=project;projectDialogOpen.value=true}
function openKnowledgeLibrary(project:WebChatProject){knowledgeProject.value=project;knowledgeLibraryOpen.value=true}
function onProjectSaved(project:WebChatProject){const index=projects.value.findIndex(p=>p.id===project.id);if(index>=0)projects.value[index]=project;else projects.value.push(project);projects.value.sort((a,b)=>a.sort_order-b.sort_order);projectDialogOpen.value=false;projectFilter.value=project.id}
function onProjectDeleted(id:number){projects.value=projects.value.filter(p=>p.id!==id);sessions.value.forEach(s=>{if(s.project_id===id){s.project_id=null;s.project_name=''}});if(projectFilter.value===id)projectFilter.value=null;projectDialogOpen.value=false}
function applyTemplate(content:string,templateID:number){draft.value=content;activeTemplateId.value=templateID;templateDialogOpen.value=false;void nextTick(()=>document.querySelector<HTMLTextAreaElement>('.composer-input')?.focus())}
async function startDraftSession(){if(sending.value||agent.creating.value)return;selectionVersion++;messagesLoading.value=false;operationError.value='';if(agent.pending.value)clearPendingDocuments();else await discardPendingDocuments();await router.replace({query:{...route.query,session:undefined}});activeSessionId.value=null;messages.value=[];streamingText.value='';streamingSources.value=[];sessionsOpen.value=false;const p=selectedProject.value;if(p?.default_group_id)selectedGroupId.value=p.default_group_id;if(p?.default_model)selectedModel.value=p.default_model;activeTemplateId.value=p?.default_template_id??null}
async function createSessionForCurrentSelection():Promise<WebChatSession|null>{
 if(pendingSession)return pendingSession
 if(!selectedGroupId.value||!selectedModel.value)return null
 const groupID=selectedGroupId.value,model=selectedModel.value
 creatingSession.value=true
 pendingSession=(async()=>{
  const session=await webChatAPI.createSession({group_id:groupID,model,project_id:selectedProject.value?.id??null,default_template_id:activeTemplateId.value})
  localStorage.setItem(draftKey(session.id),draft.value)
  localStorage.removeItem(draftKey(activeSessionId.value))
  sessions.value=[session,...sessions.value];activeSessionId.value=session.id
  await router.replace({query:{...route.query,session:String(session.id)}})
  applySessionSettings(session)
  if(options.value.projects_enabled)await loadProjects()
  return session
 })()
 try{return await pendingSession}finally{pendingSession=null;creatingSession.value=false}
}
async function selectSession(session:WebChatSession){
 if(sending.value||agent.creating.value)return
 artifactSelection++
 const version=++selectionVersion
 workspaceSection.value='chat';messagesLoading.value=true;operationError.value=''
 try{
  if(activeSessionId.value!==session.id){if(agent.pending.value)clearPendingDocuments();else await discardPendingDocuments()}
  if(version!==selectionVersion)return
  activeSessionId.value=session.id;messages.value=[];selectedGroupId.value=session.group_id;selectedModel.value=session.model
  activeTemplateId.value=session.default_template_id??null;applySessionSettings(session);sessionsOpen.value=false
  await router.replace({query:{...route.query,session:String(session.id)}})
  const loaded=await webChatAPI.listMessages(session.id)
  if(version===selectionVersion){messages.value=loaded;followOutput.value=true;await scrollToBottom()}
 }catch(e){if(version===selectionVersion)operationError.value=extractApiErrorMessage(e)}
 finally{if(version===selectionVersion)messagesLoading.value=false}
}
function applySessionSettings(session:WebChatSession){systemPrompt.value=session.system_prompt||'';temperatureInput.value=session.temperature==null?'':String(session.temperature);maxOutputTokens.value=session.max_output_tokens||8192}
async function renameSession(session:WebChatSession){const title=window.prompt(t('webChat.renamePrompt'),session.title||session.model)?.trim();if(!title)return;const updated=await webChatAPI.patchSession(session.id,{title});Object.assign(session,updated)}
async function togglePin(session:WebChatSession){await webChatAPI.patchSession(session.id,{pinned:!session.pinned_at});await refreshSessions()}
async function moveActiveSession(event:Event){const session=activeSession.value;if(!session)return;const raw=(event.target as HTMLSelectElement).value;const updated=await webChatAPI.patchSession(session.id,{project_id:raw?Number(raw):null});Object.assign(session,updated);await loadProjects()}
async function removeCurrentSession(){const s=activeSession.value;if(!s||!window.confirm(t('webChat.deleteConfirm')))return;await webChatAPI.deleteSession(s.id);localStorage.removeItem(draftKey(s.id));activeSessionId.value=null;messages.value=[];await refreshSessions();if(sessions.value[0])await selectSession(sessions.value[0])}
async function saveAdvancedSettings(){const s=activeSession.value;if(!s)return;const temp=temperatureInput.value.trim()===''?null:Number(temperatureInput.value);if(temp!==null&&(!Number.isFinite(temp)||temp<0||temp>2)){window.alert(t('webChat.invalidTemperature'));return}if(maxOutputTokens.value<1||maxOutputTokens.value>32768){window.alert(t('webChat.invalidMaxTokens'));return}savingSettings.value=true;try{const updated=await webChatAPI.patchSession(s.id,{system_prompt:systemPrompt.value,temperature:temp,max_output_tokens:maxOutputTokens.value});Object.assign(s,updated)}finally{savingSettings.value=false}}
async function toggleKnowledge(event:Event){const s=activeSession.value;if(!s)return;const updated=await webChatAPI.patchSession(s.id,{knowledge_enabled:(event.target as HTMLInputElement).checked});Object.assign(s,updated)}


async function send(){
 const content=draft.value.trim()
 if(!canSend.value||!content)return
 if(taskMode.value!=='chat'){await createFileTask(content);return}
 operationError.value=''
 try{
  const session=activeSession.value||await createSessionForCurrentSelection()
  if(!session)return
  const templateID=activeTemplateId.value
  const documentIDs=pendingDocuments.value.map(d=>d.id)
  await runGeneration(handlers=>webChatAPI.streamMessage(session.id,{content,group_id:selectedGroupId.value,model:selectedModel.value,template_id:templateID,knowledge_enabled:session.knowledge_enabled,document_ids:documentIDs},handlers),{role:'user',content},()=>{
   if(draft.value.trim()===content)draft.value=''
   activeTemplateId.value=null
   clearPendingDocuments()
  })
 }catch(e){operationError.value=extractApiErrorMessage(e)}
}
function setTaskMode(mode:TaskMode){taskMode.value=mode;if(sourceArtifact.value?.kind!==mode)sourceArtifact.value=null}
async function refreshTaskOptions(){try{options.value=await webChatAPI.getOptions()}catch(e){operationError.value=extractApiErrorMessage(e)}}
async function inspectArtifact(id:number){const version=++artifactSelection;try{const artifact=await getArtifact(id);if(version===artifactSelection)selectedArtifact.value=artifact}catch(e){if(version===artifactSelection)operationError.value=extractApiErrorMessage(e)}}
function navigateWorkspace(section:'files'|'projects'){if(sending.value||agent.creating.value)return;artifactSelection++;selectedArtifact.value=null;workspaceSection.value=section;sessionsOpen.value=false}
async function openArtifactConversation(artifact:WebAgentArtifact){
 if(sending.value||agent.creating.value)return false
 const version=++artifactSelection
 try{
  let session=sessions.value.find(s=>s.id===artifact.session_id)
  if(!session){session=await webChatAPI.getSession(artifact.session_id);if(version!==artifactSelection)return false;sessions.value=[session,...sessions.value]}
  const selection=selectSession(session),ticket=selectionVersion
  await selection
  const selected=ticket===selectionVersion&&activeSessionId.value===artifact.session_id&&workspaceSection.value==='chat'
  if(selected)taskMode.value=artifact.kind
  return selected
 }catch(e){if(version===artifactSelection)operationError.value=extractApiErrorMessage(e);return false}
}
async function reviseArtifact(artifact:WebAgentArtifact){
 if(workspaceSection.value!=='chat'||activeSessionId.value!==artifact.session_id){if(!await openArtifactConversation(artifact))return}
 sourceArtifact.value=artifact;taskMode.value=artifact.kind;selectedArtifact.value=artifact
 void nextTick(()=>document.querySelector<HTMLTextAreaElement>('.composer-input')?.focus())
}
function artifactDeleted(id:number){agent.markArtifactDeleted(id);artifactLibrary.value?.remove(id);libraryFiles.value=libraryFiles.value.filter(a=>a.id!==id);if(selectedArtifact.value?.id===id)selectedArtifact.value=null;if(sourceArtifact.value?.id===id)sourceArtifact.value=null}
function acceptFileTask(task:WebAgentTask|null){if(!task)return;if(activeSessionId.value===task.session_id){if(draft.value.trim()===task.prompt)draft.value='';clearPendingDocuments();sourceArtifact.value=null}}
async function createFileTask(content:string){
 const kind=taskMode.value,group=selectedGroupId.value,model=selectedModel.value,source=sourceArtifact.value,template=activeTemplateId.value,documents=pendingDocuments.value.map(d=>d.id)
 if(kind==='chat'||!group)return
 try{const session=activeSession.value||await createSessionForCurrentSelection();if(!session)return;acceptFileTask(await agent.create({kind,prompt:content,group_id:group,model,document_ids:documents,...(template?{template_id:template}:{}),...(source?{source_artifact_id:source.id}:{})}))}catch(e){operationError.value=extractApiErrorMessage(e)}
}
async function retryTaskSubmission(){try{acceptFileTask(await agent.retryPending())}catch(e){operationError.value=extractApiErrorMessage(e)}}
async function cancelFileTask(id:number){try{await agent.cancel(id)}catch(e){operationError.value=extractApiErrorMessage(e)}}
async function loadTaskDetails(id:number){try{await agent.loadEvents(id)}catch(e){operationError.value=extractApiErrorMessage(e)}}
async function regenerateMessage(message:WebChatMessage){const s=activeSession.value;if(!s)return;await runGeneration(handlers=>webChatAPI.regenerateMessage(s.id,message.id,handlers))}
async function reviseMessage(message:WebChatMessage){const content=window.prompt(t('webChat.revisePrompt'),message.content)?.trim();const s=activeSession.value;if(!s||!content||content===message.content)return;await runGeneration(handlers=>webChatAPI.reviseMessage(s.id,message.id,content,handlers))}
async function switchVersion(message:WebChatMessage,direction:-1|1){const s=activeSession.value;if(!s||sending.value)return;const versions=await webChatAPI.listMessageVersions(s.id,message.id);const current=versions.findIndex(v=>v.id===message.id);const target=versions[current+direction];if(!target)return;messages.value=await webChatAPI.activateMessageVersion(s.id,target.id);await scrollToBottom()}
function versionReason(reason:WebChatMessage['version_reason']){return reason==='regenerate'?t('webChat.versionRegenerated'):reason==='edit'?t('webChat.versionEdited'):t('webChat.versionOriginal')}
async function runGeneration(request:(handlers:WebChatStreamHandlers)=>Promise<void>,optimistic?:{role:'user';content:string},onAccepted?:()=>void){
 const sessionID=activeSessionId.value
 if(!sessionID||sending.value)return
 if(optimistic)messages.value.push(localMessage(sessionID,optimistic.role,optimistic.content))
 sending.value=true;operationError.value='';streamingText.value='';streamingSources.value=[];followOutput.value=true
 abortController.value=new AbortController()
 let accepted=false,terminalMessage:WebChatMessage|undefined
 await scrollToBottom()
 try{
  await request({
   signal:abortController.value.signal,
   onMeta(){if(!accepted){accepted=true;onAccepted?.()}},
   onDelta(text){streamingText.value+=text;void scrollToBottom()},
   onSources(sources){streamingSources.value=sources},
   onDone(result){terminalMessage=result.message},
   onError(message,persisted){operationError.value=message;terminalMessage=persisted},
  })
 }catch(e){if((e as Error).name!=='AbortError')operationError.value=extractApiErrorMessage(e)}
 finally{
  const partialText=streamingText.value
  try{messages.value=await webChatAPI.listMessages(sessionID)}
  catch(e){
   operationError.value=extractApiErrorMessage(e)
   if(terminalMessage)messages.value=[...messages.value.filter(m=>m.id!==terminalMessage!.id),terminalMessage]
   else if(partialText)messages.value.push({...localMessage(sessionID,'assistant',partialText),status:'partial',error_message:t('webChat.streamError')})
  }
  sending.value=false;abortController.value=null;streamingText.value='';streamingSources.value=[]
  await refreshSessions();await scrollToBottom()
 }
}
function stopGeneration(){abortController.value?.abort()}
function localMessage(sessionID:number,role:'user'|'assistant',content:string):WebChatMessage{const now=new Date().toISOString(),id=-Date.now();return{id,session_id:sessionID,user_id:authStore.user?.id||0,role,content,status:'completed',input_tokens:0,output_tokens:0,cache_read_tokens:0,cache_creation_tokens:0,logical_id:id,version_index:1,version_count:1,version_reason:'original',sources:[],created_at:now,updated_at:now}}
function quoteMessage(message:WebChatMessage){draft.value=`${message.content.split('\n').map(line=>`> ${line}`).join('\n')}\n\n${draft.value}`;void nextTick(()=>document.querySelector<HTMLTextAreaElement>('.composer-input')?.focus())}
async function copyText(text:string){await navigator.clipboard.writeText(text)}
function exportConversation(format:'markdown'|'json'){const s=activeSession.value;if(!s)return;const displayModel=sessionModelLabel(s);const content=format==='json'?JSON.stringify({session:s,messages:messages.value},null,2):[`# ${s.title||displayModel}`,`> ${s.group_name||groupName(s.group_id)} · ${displayModel}`,'',...messages.value.flatMap(m=>[`## ${m.role==='user'?'User':'Assistant'}`,m.content,''])].join('\n');const blob=new Blob([content],{type:format==='json'?'application/json':'text/markdown'});const url=URL.createObjectURL(blob);const a=document.createElement('a');a.href=url;a.download=`web-chat-${s.id}.${format==='json'?'json':'md'}`;a.click();URL.revokeObjectURL(url)}

function draftKey(id:number|null){return`subapis.webChat.draft.${authStore.user?.id||'anonymous'}.${id??'new'}`}
function groupName(id:number){return options.value.groups.find(g=>g.id===id)?.name||`#${id}`}
function hasUsage(m:WebChatMessage){return m.input_tokens+m.output_tokens+m.cache_read_tokens+m.cache_creation_tokens>0}
function formatTokens(value:number){return new Intl.NumberFormat(undefined,{notation:'compact',maximumFractionDigits:1}).format(value||0)}
async function scrollToBottom(){await nextTick();if(messageListRef.value&&followOutput.value)messageListRef.value.scrollTop=messageListRef.value.scrollHeight}
</script>

<style scoped>
.web-chat-shell{display:flex;flex:1;min-height:0;position:relative}
.reference-library{border-top:1px solid var(--wa-line);padding-top:1rem;margin-top:2rem;font-size:.875rem}.reference-library summary{cursor:pointer;font-weight:600}.reference-library p{margin:.75rem 0;color:var(--wa-muted)}.reference-library button{display:inline-flex;gap:.5rem;align-items:center;padding:.4rem .7rem;margin:.25rem .5rem .25rem 0;border:1px solid var(--wa-line);border-radius:5px;min-height:2rem}
.chat-capability-hint{font-size:.8125rem;color:var(--wa-muted);padding:.4rem 1.5rem}.task-context summary{cursor:pointer}
.task-modebar{display:flex;flex-wrap:wrap;gap:.4rem;padding:.6rem 1.5rem;border-bottom:1px solid var(--wa-line)}.task-modebar button{font-size:.875rem;padding:.25rem .7rem;min-height:2rem;border-radius:5px;color:var(--wa-muted)}.task-modebar [aria-pressed=true]{background:var(--wa-soft);color:var(--wa-text);font-weight:600}.task-modebar button:active{background:var(--wa-active)}.task-context{padding:.5rem 1.5rem;font-size:.875rem;color:var(--wa-muted);border-bottom:1px solid var(--wa-line)}.task-context p+p{margin-top:.4rem}.task-context button{color:var(--wa-accent);margin-left:.5rem}.task-context [role=alert]{color:#b91c1c}
.session-panel{width:15rem;flex-shrink:0;border-right:1px solid var(--wa-line);padding:1rem;display:flex;flex-direction:column;overflow-y:auto;gap:.5rem;background:var(--wa-bg)}
.sidebar-top{display:flex;gap:.4rem}.new-chat,.icon-button,.model-trigger{display:inline-flex;align-items:center;justify-content:center;gap:.5rem;border-radius:6px;min-height:2rem;font-size:.875rem;padding:.25rem .55rem}
.new-chat{border:1px solid var(--wa-line)}.sidebar-top>.new-chat{flex:1;justify-content:flex-start}.new-chat:hover,.icon-button:hover,.model-trigger:hover{background:var(--wa-soft)}
.section-label,.section-heading{margin-top:1.5rem;font-size:.8125rem;color:var(--wa-muted)}.section-heading{display:flex;justify-content:space-between;align-items:center}
.search-box{display:flex;gap:.5rem;align-items:center;margin-top:.5rem;padding:.3rem;color:var(--wa-muted)}.search-box input{background:transparent;width:100%;min-width:0;font-size:.875rem;outline:none}
.session-list{display:flex;flex-direction:column;gap:.2rem;flex-shrink:0}.session-item{display:flex;align-items:center;gap:.3rem;padding:.3rem .4rem;border-radius:5px}.session-item.active{background:var(--wa-active)}.session-select{display:flex;gap:.3rem;text-align:left;flex:1;min-width:0;font-size:.875rem}.session-select span{white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.session-actions{display:flex;gap:.3rem;opacity:0}.session-item:hover .session-actions,.session-item:focus-within .session-actions{opacity:1}.session-actions button{min-width:1.5rem;min-height:1.5rem;font-size:.75rem;color:var(--wa-muted)}
.project-row{display:flex;align-items:center}.project-item{display:flex;align-items:center;gap:.6rem;font-size:.875rem;padding:.35rem .4rem;width:100%;text-align:left;border-radius:5px}.project-item span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.project-item.active{background:var(--wa-soft)}
.assistant-entry,.sidebar-footer{display:flex;align-items:center;gap:.65rem;font-size:.875rem;padding:.5rem .4rem}.assistant-entry{margin-top:1rem}.sidebar-footer{margin-top:auto;padding-top:2rem;color:var(--wa-muted)}
.chat-panel{flex:1;min-width:0;min-height:0;display:flex;flex-direction:column;outline:none}.chat-header{display:flex;align-items:center;gap:.6rem;padding:.8rem 1.5rem;border-bottom:1px solid var(--wa-line);flex-shrink:0}.chat-heading{flex:1;min-width:0}.chat-heading h1{font-weight:650;font-size:1.125rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.chat-heading p{font-size:.8125rem;color:var(--wa-muted)}.model-trigger{max-width:17rem}.model-trigger span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.model-trigger>svg:first-child{color:var(--wa-accent)}
.header-menu{position:relative}.header-menu summary{list-style:none;cursor:pointer}.header-menu>div{position:absolute;right:0;top:2.5rem;z-index:20;background:var(--wa-bg);border:1px solid var(--wa-line);border-radius:6px;padding:.4rem;min-width:12rem;box-shadow:0 8px 24px #0001}.header-menu button{display:block;width:100%;padding:.5rem;text-align:left;font-size:.875rem}.danger,.message-error{color:#b91c1c}
.home-scroll{overflow-y:auto;flex:1}.message-list{overflow-y:auto;flex:1;min-height:0;padding:1.5rem max(1.5rem,calc((100% - 52rem)/2));scrollbar-gutter:stable}.message-row{display:flex;margin-bottom:1.75rem}.message-row.user{justify-content:flex-end}.message-stack{max-width:100%;width:100%;min-width:0}.user .message-stack{width:auto;max-width:88%}.message-bubble.user{background:var(--wa-soft);border-radius:8px;padding:.65rem 1rem}.message-bubble.assistant{padding:.5rem 0}.assistant-name{display:flex;align-items:center;gap:.65rem;font-weight:650;margin-bottom:.75rem;font-size:.875rem}.assistant-name>span{display:grid;place-items:center;width:1.75rem;height:1.75rem;border-radius:50%;background:var(--wa-text);color:var(--wa-bg);font-weight:500}
.message-actions{display:flex;flex-wrap:wrap;align-items:center;gap:.8rem;margin-top:.6rem;font-size:.8125rem;color:var(--wa-muted)}.message-actions button{display:flex;gap:.3rem;align-items:center}.usage-details{position:relative}.usage-details summary{cursor:pointer}.usage-details>div{display:flex;flex-direction:column;border:1px solid var(--wa-line);padding:.75rem;border-radius:6px;margin-top:.4rem;background:var(--wa-soft);overflow-wrap:anywhere}.version-switch{display:flex;align-items:center;gap:.5rem;font-size:.8125rem;color:var(--wa-muted);margin-top:.5rem}.version-switch button{padding:.1rem .5rem}
.composer-dock{width:min(100%,55rem);align-self:center;padding:.75rem 1.5rem 1.25rem;background:var(--wa-bg)}.empty-state{min-height:18rem;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.7rem;color:var(--wa-muted);text-align:center}.empty-state h2{font-size:1.125rem;font-weight:650;color:var(--wa-text)}.empty-small{font-size:.8125rem;color:var(--wa-muted);padding:.5rem}
.workspace-error{margin:.75rem 1.5rem;padding:.7rem 1rem;color:#b91c1c;background:#fef2f2;font-size:.875rem;border-radius:6px}.workspace-error button{float:right;text-decoration:underline}
.context-card{display:flex;flex-direction:column;gap:1rem}.context-card label{display:flex;flex-direction:column;gap:.4rem;font-size:.875rem}.context-card .input{border:1px solid #d4d4d8;border-radius:6px;padding:.5rem;background:transparent;width:100%}.context-card .knowledge-toggle{flex-direction:row;justify-content:space-between}.advanced-settings summary{cursor:pointer;font-weight:600}.advanced-settings label{margin:.75rem 0}.pricing-card{border-top:1px solid #e4e4e7;padding-top:.75rem;font-size:.875rem}.pricing-card h3{font-weight:600;margin-bottom:.5rem}.pricing-card>div{display:flex;justify-content:space-between;gap:1rem}
.projects-workspace{overflow:auto;padding:2rem;flex:1}.projects-workspace>p{margin-bottom:1rem;color:var(--wa-muted)}.project-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(15rem,1fr));gap:1rem;margin-top:1.5rem}.project-grid article{border:1px solid var(--wa-line);border-radius:8px;padding:1rem}.project-grid h2{font-weight:650;margin:.5rem 0}.project-grid p{color:var(--wa-muted);font-size:.875rem;min-height:2rem}.project-grid article>div{display:flex;flex-wrap:wrap;gap:1rem;margin-top:1rem;font-size:.875rem;color:var(--wa-accent)}
.session-toggle,.panel-scrim{display:none}.typing{color:var(--wa-muted);font-size:.875rem}
@media(max-width:900px){.session-panel{position:fixed;top:3.5rem;left:0;bottom:0;z-index:60;width:min(18rem,85vw);transform:translateX(-100%);transition:transform .18s}.session-panel-open{transform:translateX(0)}.session-toggle{display:inline-flex}.panel-scrim{display:block;position:fixed;inset:3.5rem 0 0;background:#0004;z-index:55}.chat-header{padding:.75rem 1rem}.model-trigger{max-width:12rem}.session-actions{opacity:1}}
@media(max-width:767px){.message-list{padding:1.25rem 1rem}.composer-dock{padding:.5rem .75rem max(.5rem,env(safe-area-inset-bottom))}.new-chat,.icon-button,.model-trigger,.message-actions button,.project-item{min-height:44px}.model-trigger{font-size:.8125rem;max-width:10rem}.projects-workspace{padding:1rem}}
</style>
