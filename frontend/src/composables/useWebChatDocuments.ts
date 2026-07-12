import { ref, type Ref } from 'vue'
import webChatAPI, { type WebChatDocument, type WebChatSession } from '@/api/webChat'
import { extractApiErrorMessage } from '@/utils/apiError'

export function useWebChatDocuments(ensureSession: () => Promise<WebChatSession | null>, translate: (key: string) => string) {
  const pendingDocuments: Ref<WebChatDocument[]> = ref([])
  const attachmentState = ref('')

  async function uploadTemporaryDocuments(files: File[]) {
    if (!files.length || attachmentState.value) return
    const session = await ensureSession()
    if (!session) return
    for (const file of files) {
      attachmentState.value = `${translate('webChat.uploading')} ${file.name} · 0%`
      try {
        const uploaded = await webChatAPI.uploadSessionDocument(session.id, file, percent => {
          attachmentState.value = `${translate('webChat.uploading')} ${file.name} · ${percent}%`
        })
        attachmentState.value = `${translate('webChat.documentProcessing')} ${file.name}`
        pendingDocuments.value.push(await waitUntilReady(uploaded.id, translate))
      } catch (error) {
        window.alert(extractApiErrorMessage(error))
      } finally {
        attachmentState.value = ''
      }
    }
  }

  function removePendingDocument(id: number) { pendingDocuments.value = pendingDocuments.value.filter(item => item.id !== id) }
  function clearPendingDocuments() { pendingDocuments.value = [] }
  return { pendingDocuments, attachmentState, uploadTemporaryDocuments, removePendingDocument, clearPendingDocuments }
}

async function waitUntilReady(id: number, translate: (key: string) => string): Promise<WebChatDocument> {
  for (let attempt = 0; attempt < 60; attempt++) {
    const document = await webChatAPI.getDocument(id)
    if (document.status === 'ready') return document
    if (document.status === 'failed') throw new Error(document.error_message || translate('webChat.documentFailed'))
    await new Promise(resolve => setTimeout(resolve, 1000))
  }
  throw new Error(translate('webChat.documentProcessingTimeout'))
}
