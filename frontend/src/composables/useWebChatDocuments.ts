import { ref, type Ref } from 'vue'
import webChatAPI, { type WebChatDocument, type WebChatSession } from '@/api/webChat'
import { extractApiErrorMessage } from '@/utils/apiError'

export interface WebChatFailedAttachment {
  key: string
  file: File
  error: string
  documentId?: number
}

export function useWebChatDocuments(ensureSession: () => Promise<WebChatSession | null>, translate: (key: string) => string) {
  const pendingDocuments: Ref<WebChatDocument[]> = ref([])
  const failedAttachments: Ref<WebChatFailedAttachment[]> = ref([])
  const attachmentState = ref('')

  async function uploadTemporaryDocuments(files: File[]) {
    if (!files.length || attachmentState.value) return
    const session = await ensureSession()
    if (!session) return
    for (const file of files) {
      await uploadOne(session.id, file)
    }
  }

  async function uploadOne(sessionID: number, file: File, existingDocumentID?: number) {
    let documentID = existingDocumentID
    attachmentState.value = `${translate('webChat.uploading')} ${file.name} · 0%`
    try {
      if (!documentID) {
        const uploaded = await webChatAPI.uploadSessionDocument(sessionID, file, percent => {
          attachmentState.value = `${translate('webChat.uploading')} ${file.name} · ${percent}%`
        })
        documentID = uploaded.id
      } else {
        const existing = await webChatAPI.getDocument(documentID)
        if (existing.status === 'failed') await webChatAPI.retryDocument(documentID)
      }
      attachmentState.value = `${translate('webChat.documentProcessing')} ${file.name}`
      pendingDocuments.value.push(await waitUntilReady(documentID, translate))
    } catch (error) {
      failedAttachments.value.push({ key: `${Date.now()}-${Math.random()}`, file, documentId: documentID, error: extractApiErrorMessage(error) })
    } finally {
      attachmentState.value = ''
    }
  }

  async function retryFailedAttachment(key: string) {
    if (attachmentState.value) return
    const failed = failedAttachments.value.find(item => item.key === key)
    if (!failed) return
    const session = await ensureSession()
    if (!session) return
    failedAttachments.value = failedAttachments.value.filter(item => item.key !== key)
    await uploadOne(session.id, failed.file, failed.documentId)
  }

  async function removePendingDocument(id: number) {
    pendingDocuments.value = pendingDocuments.value.filter(item => item.id !== id)
    await webChatAPI.deleteDocument(id).catch(() => undefined)
  }

  async function removeFailedAttachment(key: string) {
    const failed = failedAttachments.value.find(item => item.key === key)
    failedAttachments.value = failedAttachments.value.filter(item => item.key !== key)
    if (failed?.documentId) await webChatAPI.deleteDocument(failed.documentId).catch(() => undefined)
  }

  function clearPendingDocuments() { pendingDocuments.value = [] }
  async function discardPendingDocuments() {
    const ids = [
      ...pendingDocuments.value.map(item => item.id),
      ...failedAttachments.value.flatMap(item => item.documentId ? [item.documentId] : []),
    ]
    pendingDocuments.value = []
    failedAttachments.value = []
    await Promise.all(ids.map(id => webChatAPI.deleteDocument(id).catch(() => undefined)))
  }
  return { pendingDocuments, failedAttachments, attachmentState, uploadTemporaryDocuments, retryFailedAttachment, removePendingDocument, removeFailedAttachment, clearPendingDocuments, discardPendingDocuments }
}

async function waitUntilReady(id: number, translate: (key: string) => string): Promise<WebChatDocument> {
  for (let attempt = 0; attempt < 300; attempt++) {
    const document = await webChatAPI.getDocument(id)
    if (document.status === 'ready') return document
    if (document.status === 'failed') throw new Error(document.error_message || translate('webChat.documentFailed'))
    await new Promise(resolve => setTimeout(resolve, 1000))
  }
  throw new Error(translate('webChat.documentProcessingTimeout'))
}
