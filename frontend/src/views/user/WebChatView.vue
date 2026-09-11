<template>
  <WebChatWorkbench :section="workspaceSection" :busy="sending || creatingSession || agent.creating.value" :projects-enabled="options.projects_enabled" :files-enabled="options.files_enabled" :artifacts-enabled="artifactsEnabled" :templates-enabled="options.templates_enabled" @home="goHome" @navigate="navigateWorkspace" @templates="templateDialogOpen=true">
    <div class="web-chat-shell">
      <aside class="session-panel wc-sessions" :class="{ 'is-open': sessionsOpen }">
        <div class="wc-sess-head"><h2>{{ t('webChat.sessions') }}</h2><div class="wc-sess-actions"><button class="wc-ib wc-sess-close" :aria-label="t('common.close')" @click="sessionsOpen=false"><Icon name="x" size="sm" /></button><button class="wc-ib wc-ib-pri" :title="t('webChat.newChat')" :aria-label="t('webChat.newChat')" :disabled="sending" @click="goHome"><Icon name="plus" size="sm" /></button></div></div>
        <label class="search-box wc-search"><Icon name="search" size="sm" /><input v-model="sessionQuery" :aria-label="t('webChat.searchSessions')" :placeholder="t('webChat.searchSessions')" /><kbd>⌘K</kbd></label>
        <div v-if="options.projects_enabled" class="wc-chips">
          <button class="wc-chip" :class="{ on: projectFilter === 'all' }" :disabled="sending" @click="projectFilter='all'">{{ t('webChat.allChats') }}<b>{{ sessions.length }}</b></button>
          <button class="wc-chip" :class="{ on: projectFilter === null }" :disabled="sending" @click="projectFilter=null">{{ t('webChat.uncategorized') }}<b>{{ uncategorizedCount }}</b></button>
          <button v-for="project in projects" :key="project.id" class="wc-chip" :class="{ on: projectFilter === project.id }" :disabled="sending" :title="project.name" @click="chooseProject(project)">
            <i :style="{ background: project.color }" /><span class="trunc">{{ project.name }}</span><b>{{ project.session_count }}</b>
          </button>
          <button class="wc-chip wc-chip-add" :title="t('webChat.newProject')" :aria-label="t('webChat.newProject')" :disabled="sending" @click="openProjectEditor(null)"><Icon name="plus" size="xs" /></button>
        </div>
        <div v-if="options.projects_enabled && selectedProject" class="wc-proj-bar">
          <i :style="{ background: selectedProject.color }" /><strong class="trunc">{{ selectedProject.name }}</strong>
          <button v-if="options.files_enabled" class="wc-ib wc-ib-xs" :title="t('webChat.knowledgeLibrary')" :aria-label="t('webChat.knowledgeLibrary')" @click="openKnowledgeLibrary(selectedProject)"><Icon name="book" size="xs" /></button>
          <button class="wc-ib wc-ib-xs" :title="t('workspace.projectSettings')" :aria-label="t('workspace.projectSettings')" :disabled="sending" @click="openProjectEditor(selectedProject)"><Icon name="more" size="xs" /></button>
        </div>
        <div class="session-list wc-list">
          <template v-for="group in sessionGroups" :key="group.key">
            <p class="wc-grp">{{ t(`webChat.${group.label}`) }}</p>
            <article v-for="session in group.items" :key="session.id" class="session-item wc-item" :class="{active:session.id===activeSessionId, on:session.id===activeSessionId}" :style="{ '--pc': projectColor(session) }">
              <button class="session-select wc-item-main" :disabled="sending" @click="selectSession(session)"><span class="wc-item-t trunc">{{ session.title || sessionModelLabel(session) }}</span><span class="wc-item-s trunc">{{ sessionModelLabel(session) }} · {{ relativeTime(session.updated_at || session.created_at) }}</span></button>
              <span v-if="session.pinned_at" class="wc-item-pin"><Icon name="bookmark" size="xs" /></span>
              <div class="session-actions wc-item-acts"><button :aria-label="t('webChat.pin')" :disabled="sending" @click="togglePin(session)"><Icon name="bookmark" size="xs" /></button><button :aria-label="t('webChat.rename')" :disabled="sending" @click="renameSession(session)"><Icon name="edit" size="xs" /></button></div>
            </article>
          </template>
          <p v-if="!displayedSessions.length&&!loading" class="empty-small wc-empty-small">{{ t('webChat.noSessions') }}</p>
        </div>
        <button v-if="options.templates_enabled" class="assistant-entry wc-sess-entry" :disabled="sending" @click="templateDialogOpen=true"><Icon name="sparkles" size="sm" />{{ t('workspace.assistants') }}</button>
        <div class="wc-sess-foot"><span>{{ t('webChat.sessionCount', { count: sessions.length }) }}</span></div>
      </aside>
      <main id="web-agent-main" class="chat-panel" tabindex="-1">
        <header class="chat-header wc-chead">
          <button class="icon-button session-toggle wc-ib wc-sess-toggle" :aria-label="t('webChat.openSessions')" @click="sessionsOpen=true"><Icon name="menu" size="sm" /></button>
          <div class="chat-heading wc-crumb">
            <template v-if="activeSessionProject"><i class="dot" :style="{ background: activeSessionProject.color }" /><span class="trunc">{{ activeSessionProject.name }}</span><span class="sep">/</span></template>
            <h1 class="trunc"><b class="trunc">{{ workspaceSection==='chat' ? activeSession?.title || selectedProject?.name || t('workspace.home') : workspaceSection==='files' ? t('workspace.files') : t('webChat.projects') }}</b></h1>
            <button v-if="activeSession && workspaceSection==='chat'" class="wc-ib wc-ib-xs" :title="t('webChat.rename')" :aria-label="t('webChat.rename')" :disabled="sending" @click="renameSession(activeSession)"><Icon name="edit" size="xs" /></button>
          </div>
          <div class="wc-head-acts">
            <button v-if="workspaceSection==='chat'&&agent.artifacts.value.length" class="wc-tb wc-tb-o" @click="inspectArtifact(agent.artifacts.value[0]!.id)"><Icon name="layers" size="sm" /><span>{{ t('webAgent.files') }} · {{ agent.artifacts.value.length }}</span></button>
            <button v-if="workspaceSection==='chat'" class="wc-tb wc-tb-o" :aria-pressed="contextOpen" :title="t('webChat.modelSettings')" @click="contextOpen=true"><Icon name="sparkles" size="sm" /><span>{{ selectedModelOption?.display_name || selectedModel || t('webChat.selectModel') }}</span><Icon name="chevronDown" size="xs" /></button>
            <details v-if="activeSession&&workspaceSection==='chat'" class="header-menu wc-menu-wrap"><summary class="wc-tb wc-tb-o" :aria-label="t('workspace.actions')"><Icon name="download" size="sm" /><span>{{ t('webChat.export') }}</span></summary><div class="wc-menu">
              <button @click="exportConversation('markdown')"><Icon name="document" size="xs" />{{ t('webChat.exportMarkdown') }}</button>
              <button @click="exportConversation('json')"><Icon name="terminal" size="xs" />{{ t('webChat.exportJson') }}</button>
              <button class="danger" :disabled="sending" @click="removeCurrentSession"><Icon name="trash" size="xs" />{{ t('common.delete') }}</button>
            </div></details>
            <span v-if="workspaceSection==='chat'" class="wc-vsep" />
            <button v-if="workspaceSection==='chat'" class="wc-ib wc-pane-toggle" :aria-label="t('webChat.toggleInspector')" :aria-pressed="artifactPaneOpen" :title="t('webChat.toggleInspector')" @click="artifactPaneOpen=!artifactPaneOpen"><Icon name="viewColumns" size="sm" /></button>
          </div>
        </header>
        <div v-if="operationError" role="alert" class="workspace-error">{{ operationError }} <button @click="operationError=''">{{ t('common.close') }}</button></div>

        <div v-if="taskMode!=='chat'&&workspaceSection==='chat'" class="task-context">
          <p v-if="!options.tasks_enabled">{{ t('webAgent.unavailable') }} <button @click="refreshTaskOptions">{{ t('webAgent.refresh') }}</button></p>
          <p v-else>{{ t('webAgent.budget',{minutes:(options.task_limits?.deadline_seconds||600)/60}) }}</p>
          <details v-if="options.task_limits"><summary>{{ t('workspace.usageDetails') }}</summary>{{ t('webAgent.budgetDetails',{input:options.task_limits.max_input_bytes/1024,output:activeSession?.max_output_tokens||8192,file:options.task_limits.max_artifact_bytes/1024/1024}) }}</details>
          <p v-if="sourceArtifact">{{ t('webAgent.source',{version:sourceArtifact.version}) }} · {{ sourceArtifact.title }} <button @click="sourceArtifact=null">{{ t('webAgent.clearSource') }}</button></p>
          <p v-if="agent.error.value" role="alert">{{ agent.error.value }}</p>
          <p v-if="agent.pending.value">{{ t('webAgent.pending') }} <button :disabled="agent.creating.value" @click="retryTaskSubmission">{{ t('webAgent.retrySubmission') }}</button></p>
        </div>
        <WebAgentArtifactLibrary v-if="workspaceSection==='files'" ref="artifactLibrary" class="wc-lib" :enabled="artifactsEnabled" :user-id="authStore.user?.id" :sessions="sessions" @open="inspectArtifact" @conversation="openArtifactConversation" @loaded="libraryFiles=$event">
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
          <template #composer><WebChatComposer v-model="draft" :modes="taskModes" :mode="taskMode" :mode-disabled="sending||agent.creating.value" :allow-modes="workspaceSection==='chat'" @update:mode="setTaskMode" :disabled="!canCompose" :can-send="canSend&&Boolean(draft.trim())" :sending="sending" :files-enabled="options.files_enabled" :templates-enabled="options.templates_enabled" :template-name="activeTemplateName" :documents="pendingDocuments" :failed-attachments="failedAttachments" :attachment-state="attachmentState" @submit="send" @stop="stopGeneration" @open-template="templateDialogOpen=true" @clear-template="activeTemplateId=null" @files="uploadTemporaryDocuments" @remove-document="removePendingDocument" @retry-attachment="retryFailedAttachment" @remove-failed-attachment="removeFailedAttachment"/></template>
        </WebChatHome></div>
        <template v-else>
          <p v-if="taskMode==='chat'" class="chat-capability-hint">{{ t('webAgent.chatHint') }}</p>
          <template v-if="taskMode!=='chat'">
            <div class="wc-chat-body"><div class="wc-scroll"><div class="wc-colw"><WebAgentTaskFeed :tasks="agent.tasks.value" :events="agent.events.value" :artifacts="agent.artifacts.value" :has-more="Boolean(agent.nextBefore.value)" :loading="agent.loading.value" @open="inspectArtifact" @cancel="cancelFileTask" @details="loadTaskDetails" @older="agent.older" /></div></div></div>
          </template>
          <template v-else>
          <div class="wc-chat-body"><section ref="messageListRef" class="message-list wc-scroll" @scroll="onMessageScroll"><div class="wc-colw">
            <div v-if="loading || messagesLoading" class="empty-state" role="status"><Icon name="refresh" size="lg" class="animate-spin" /><h2>{{ t('common.loading') }}</h2></div>
            <div v-else-if="!enabled" class="empty-state"><Icon name="lock" size="lg" /><h2>{{ t('webChat.disabledTitle') }}</h2><p>{{ t('webChat.disabledDescription') }}</p><button v-if="operationError" @click="loadInitial">{{ t('workspace.retry') }}</button></div>
            <div v-else-if="!hasUsableModel" class="empty-state"><h2>{{ t('webChat.noGroupsTitle') }}</h2><p>{{ t('webChat.noGroupsDescription') }}</p></div>
            <div v-else-if="messages.length===0" class="empty-state"><h2>{{ t('workspace.startTitle') }}</h2></div>
            <template v-for="message in messages" :key="message.id">
              <article class="message-row wc-turn" :class="message.role">
                <span class="wc-av" :class="message.role==='user' ? 'you' : 'ai'" aria-hidden="true">{{ message.role==='user' ? t('webChat.you').slice(0,1) : 'S' }}</span>
                <div style="min-width:0">
                  <div class="wc-meta">
                    <b>{{ message.role==='user' ? t('webChat.you') : 'SubAPIs' }}</b>
                    <span>{{ formatMessageTime(message.updated_at || message.created_at) }}</span>
                    <span v-if="options.history_enabled&&message.version_count>1" class="wc-ver version-switch">
                      <button :disabled="message.version_index<=1||sending" @click="switchVersion(message,-1)" :aria-label="t('common.previous')"><Icon name="chevronLeft" size="xs" /></button>
                      <span class="n">{{ message.version_index }} / {{ message.version_count }}</span><i class="sep" /><span>{{ versionReason(message.version_reason) }}</span>
                      <button :disabled="message.version_index>=message.version_count||sending" @click="switchVersion(message,1)" :aria-label="t('common.next')"><Icon name="chevronRight" size="xs" /></button>
                    </span>
                  </div>
                  <div v-if="message.role==='user'" class="message-bubble user wc-ub"><WebChatMessageContent :content="message.content" /></div>
                  <div v-else class="message-bubble assistant wc-md"><WebChatMessageContent :content="message.content" markdown /><p v-if="message.status==='error'||message.status==='partial'" class="message-error wc-msg-error">{{ message.error_message || t('webChat.streamError') }}</p></div>
                  <WebChatSources :sources="message.sources || []" />
                  <div class="wc-tele message-footer">
                    <template v-if="message.role==='assistant'&&hasUsage(message)">
                      <span title="输入 token"><Icon name="arrowUp" size="xs" />{{ formatTokens(message.input_tokens) }}</span>
                      <span title="输出 token"><Icon name="arrowDown" size="xs" />{{ formatTokens(message.output_tokens) }}</span>
                      <span v-if="message.cache_read_tokens"><em class="lbl">cache</em>{{ formatTokens(message.cache_read_tokens) }}</span>
                    </template>
                    <span v-if="message.request_id" :title="message.request_id"><em class="lbl">ID</em>{{ shortRequestID(message.request_id) }}</span>
                  </div>
                  <div class="message-actions wc-acts">
                    <button @click="copyText(message.content)"><Icon name="copy" size="xs" />{{ t('webChat.copy') }}</button>
                    <button @click="quoteMessage(message)"><Icon name="chatBubble" size="xs" />{{ t('webChat.quote') }}</button>
                    <button v-if="message.role==='user'" :disabled="sending" @click="reviseMessage(message)"><Icon name="edit" size="xs" />{{ t('webChat.editResend') }}</button>
                    <button v-else :disabled="sending" @click="regenerateMessage(message)"><Icon name="refresh" size="xs" />{{ message.status==='error'?t('webChat.retry'):t('webChat.regenerate') }}</button>
                    <button v-if="message.role==='assistant'&&message.version_count>1" :disabled="sending" @click="switchVersion(message,-1)"><Icon name="history" size="xs" />{{ t('workspace.usageDetails') }}</button>
                  </div>
                </div>
              </article>
            </template>
            <article v-if="sending" class="message-row assistant wc-turn" role="status">
              <span class="wc-av ai">S</span>
              <div style="min-width:0">
                <div class="wc-meta"><b>SubAPIs</b><span class="wc-typing"><i />{{ t('workspace.waiting') }}</span></div>
                <div class="wc-md message-bubble assistant"><WebChatMessageContent v-if="streamingText" :content="streamingText" markdown /><p v-else class="typing">{{ t('workspace.waiting') }}…</p></div>
              </div>
            </article>
          </div></section></div>
          </template>
          <div v-if="canCompose" class="composer-dock wc-compose"><WebChatComposer v-model="draft" :modes="taskModes" :mode="taskMode" :mode-disabled="sending||agent.creating.value" :allow-modes="workspaceSection==='chat'" @update:mode="setTaskMode" :disabled="!canCompose||agent.creating.value" :can-send="canSend&&Boolean(draft.trim())" :sending="sending" :files-enabled="options.files_enabled" :templates-enabled="options.templates_enabled" :template-name="activeTemplateName" :documents="pendingDocuments" :failed-attachments="failedAttachments" :attachment-state="attachmentState" @submit="send" @stop="stopGeneration" @open-template="templateDialogOpen=true" @files="uploadTemporaryDocuments" @remove-document="removePendingDocument" @retry-attachment="retryFailedAttachment" @remove-failed-attachment="removeFailedAttachment"/></div>
        </template>
      </main>
      <WebAgentArtifactPane v-if="selectedArtifact&&workspaceSection!=='projects'" class="wc-pane" :class="{ 'is-open': artifactPaneOpen }" :artifact="selectedArtifact" :files="workspaceSection==='files'?libraryFiles:agent.artifacts.value" :can-revise="Boolean(options.tasks_enabled)&&!sending&&!agent.creating.value" @close="selectedArtifact=null" @select="inspectArtifact" @revise="reviseArtifact" @deleted="artifactDeleted" />
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
import { formatTokens } from '@/utils/webChatTokens'
import '@/styles/web-chat.css'
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
const artifactsEnabled=computed(()=>agent.artifacts.value.length>0||Boolean(options.value.tasks_enabled)||['starting','ready','unavailable'].includes(options.value.task_status||''))
const artifactPaneOpen=ref(true)
const activeSessionProject=computed(()=>projects.value.find(project=>project.id===activeSession.value?.project_id)||null)
const uncategorizedCount=computed(()=>sessions.value.filter(session=>!session.project_id).length)
const agent=useWebAgentTasks(activeSessionId,computed(()=>authStore.user?.id),computed(()=>Boolean(options.value.task_limits||options.value.tasks_enabled)))
let artifactSelection=0
const systemPrompt=ref(''), temperatureInput=ref(''), maxOutputTokens=ref(8192), messageListRef=ref<HTMLElement|null>(null)
const abortController=ref<AbortController|null>(null)
let taskOptionsTimer: ReturnType<typeof setTimeout> | undefined
const {pendingDocuments,failedAttachments,attachmentState,uploadTemporaryDocuments,retryFailedAttachment,removePendingDocument,removeFailedAttachment,clearPendingDocuments,discardPendingDocuments}=useWebChatDocuments(async()=>activeSession.value||await createSessionForCurrentSelection(),key=>t(key))

const activeSession=computed(()=>sessions.value.find(item=>item.id===activeSessionId.value)||null)
const selectedGroup=computed(()=>options.value.groups.find(group=>group.id===selectedGroupId.value)||null)
const selectedGroupModels=computed(()=>selectedGroup.value?.models||[])
const selectedModelOption=computed(()=>selectedGroupModels.value.find(model=>model.name===selectedModel.value)||null)
const hasUsableModel=computed(()=>options.value.groups.some(group=>group.models.length>0))
const displayedSessions=computed(()=>{
  const startToday=new Date().setHours(0,0,0,0)
  const rank=(session:WebChatSession)=>{ if(session.pinned_at)return 0; const ts=new Date(session.updated_at||session.created_at).getTime(); if(Number.isNaN(ts)||ts>=startToday)return 1; if(ts>=startToday-DAY)return 2; return 3 }
  return sessions.value.filter(s=>(projectFilter.value==='all'||(s.project_id??null)===projectFilter.value)&&[s.title,s.model,s.group_name||''].some(value=>value.toLowerCase().includes(sessionQuery.value.trim().toLowerCase()))).slice().sort((a,b)=>rank(a)-rank(b)||new Date(b.updated_at||b.created_at).getTime()-new Date(a.updated_at||a.created_at).getTime())
})
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

const DAY=86_400_000
const sessionGroups=computed(()=>{
  const startToday=new Date().setHours(0,0,0,0)
  const bucket=(session:WebChatSession)=>{ if(session.pinned_at)return 'pinned'; const ts=new Date(session.updated_at||session.created_at).getTime(); if(Number.isNaN(ts)||ts>=startToday)return 'today'; if(ts>=startToday-DAY)return 'yesterday'; return 'earlier' }
  const order:['pinned'|'today'|'yesterday'|'earlier',string][]=[['pinned','groupPinned'],['today','groupToday'],['yesterday','groupYesterday'],['earlier','groupEarlier']]
  const sorted=displayedSessions.value.slice().sort((a,b)=>new Date(b.updated_at||b.created_at).getTime()-new Date(a.updated_at||a.created_at).getTime())
  return order.map(([key,label])=>({key,label,items:sorted.filter(session=>bucket(session)===key)})).filter(group=>group.items.length)
})
function projectColor(session: WebChatSession): string { return projects.value.find(project => project.id === session.project_id)?.color || 'transparent' }
function relativeTime(value: string): string {
  const date=new Date(value)
  if(Number.isNaN(date.getTime()))return ''
  const diff=Date.now()-date.getTime()
  if(diff>=0&&diff<60_000)return t('webChat.justNow')
  if(diff>=0&&diff<3_600_000)return t('webChat.minutesAgo',{count:Math.max(1,Math.floor(diff/60_000))})
  const startToday=new Date().setHours(0,0,0,0)
  const time=new Intl.DateTimeFormat(locale.value,{hour:'2-digit',minute:'2-digit'}).format(date)
  if(date.getTime()>=startToday)return time
  if(date.getTime()>=startToday-DAY)return `${t('webChat.yesterday')} ${time}`
  return new Intl.DateTimeFormat(locale.value,{month:'short',day:'numeric'}).format(date)
}
function formatMessageTime(value: string): string { const date=new Date(value); return Number.isNaN(date.getTime())?'':new Intl.DateTimeFormat(locale.value,{hour:'2-digit',minute:'2-digit'}).format(date) }
function shortRequestID(id: string): string { return id.length>16?`${id.slice(0,8)}…${id.slice(-6)}`:id }

function sessionModelLabel(session: WebChatSession): string {
  const platform = session.platform || options.value.groups.find(group => group.id === session.group_id)?.platform
  const option = options.value.groups.find(group => group.id === session.group_id)?.models.find(model => model.name === session.model)
  return option?.display_name || modelDisplayName(session.model, platform)
}

watch(selectedGroupId,()=>{if(!selectedGroupModels.value.some(m=>m.name===selectedModel.value))selectedModel.value=selectedGroupModels.value[0]?.name||''})
watch(draft,value=>localStorage.setItem(draftKey(activeSessionId.value),value))
watch(activeSessionId,id=>{draft.value=localStorage.getItem(draftKey(id))||''})
watch(()=>options.value.task_status,status=>{
 clearTimeout(taskOptionsTimer)
 if(status==='starting') taskOptionsTimer=setTimeout(()=>{void refreshTaskOptions()},2000)
})
watch(activeSessionId,()=>{artifactSelection++;selectedArtifact.value=null;sourceArtifact.value=null})
watch(projectFilter,()=>{if(!activeSession.value)startDraftSession()})
onMounted(()=>{void loadInitial()})
onBeforeUnmount(()=>{selectionVersion++;artifactSelection++;clearTimeout(taskOptionsTimer);abortController.value?.abort();if(agent.pending.value)clearPendingDocuments();else void discardPendingDocuments()})

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

async function scrollToBottom(){await nextTick();if(messageListRef.value&&followOutput.value)messageListRef.value.scrollTop=messageListRef.value.scrollHeight}
</script>

<style scoped>
/* 布局与组件材质统一放在 src/styles/web-chat.css；此处只保留仍在使用中的旧类名映射。 */
.workspace-error{margin:.75rem 1.5rem;padding:.7rem 1rem;color:#b91c1c;background:#fef2f2;font-size:.875rem;border-radius:6px}
.workspace-error button{float:right;text-decoration:underline}
.reference-library{border-top:1px solid var(--wc-line);padding-top:1rem;margin-top:2rem;font-size:.8125rem}
.reference-library summary{cursor:pointer;font-weight:600}
.reference-library p{margin:.75rem 0;color:var(--wc-ink3)}
.reference-library button{display:inline-flex;gap:.5rem;align-items:center;padding:.4rem .7rem;margin:.25rem .5rem .25rem 0;border:1px solid var(--wc-line-s);border-radius:9px;min-height:2rem}
.projects-workspace{overflow:auto;padding:2rem;flex:1}
.projects-workspace>p{margin-bottom:1rem;color:var(--wc-ink3)}
.project-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(15rem,1fr));gap:1rem;margin-top:1.5rem}
.project-grid article{border:1px solid var(--wc-line-s);border-radius:12px;padding:1rem;box-shadow:var(--wc-sh1),var(--wc-hl)}
.project-grid h2{font-weight:650;margin:.5rem 0}
.project-grid p{color:var(--wc-ink3);font-size:.875rem;min-height:2rem}
.project-grid article>div{display:flex;flex-wrap:wrap;gap:1rem;margin-top:1rem;font-size:.875rem;color:var(--wc-acc-d)}
</style>
