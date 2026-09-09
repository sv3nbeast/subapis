import workerURL from 'pdfjs-dist/build/pdf.worker.min.mjs?url'

export async function openWebAgentPDF(blob: Blob) {
  if (blob.size > 32 * 1024 * 1024) throw new Error('Preview exceeds the file budget')
  const data = new Uint8Array(await blob.arrayBuffer())
  if (new TextDecoder().decode(data.subarray(0, 5)) !== '%PDF-') throw new Error('Invalid PDF preview')
  const pdfjs = await import('pdfjs-dist')
  pdfjs.GlobalWorkerOptions.workerSrc = workerURL
  // Canvas + extracted text only: no PDF scripting, forms, actions, embedded
  // browser document or external links are mounted into the application origin.
  // PDF.js 6 removed the old isEvalSupported option with its eval-based compiler.
  return pdfjs.getDocument({ data, enableXfa: false, useWorkerFetch: false, stopAtErrors: true })
}
