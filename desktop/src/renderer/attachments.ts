import type { HistoryAttachment } from './harness-types'

export type Attachment = { name: string; mediaType: string; data: string; size: number }

const formats: Record<string, string> = {
  txt: 'text/plain', md: 'text/markdown', markdown: 'text/markdown', csv: 'text/csv',
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg',
}

const mimeToExt: Record<string, string> = {
  'text/plain': 'txt',
  'text/markdown': 'md',
  'text/csv': 'csv',
  'image/png': 'png',
  'image/jpeg': 'jpg',
}

function utf8Base64(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000))
  }
  return btoa(binary)
}

export function restoreHistoryAttachments(files: HistoryAttachment[]): Attachment[] {
  return files.map((file) => {
    if (file.image) {
      const match = /^data:(image\/(?:png|jpeg));base64,([a-zA-Z0-9+/=]+)$/.exec(file.image)
      if (!match) throw new Error(`Cannot resend ${file.name}: the saved image data is unavailable.`)
      const mediaType = match[1]
      const data = match[2]
      const padding = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0
      const size = Math.floor(data.length * 3 / 4) - padding
      const ext = mimeToExt[mediaType]
      const name = /\.[a-z0-9]+$/i.test(file.name) ? file.name : `${file.name || 'Image'}.${ext}`
      return { name, mediaType, data, size }
    }

    const ext = file.name.includes('.') ? file.name.split('.').at(-1)?.toLowerCase() ?? '' : ''
    const mediaType = formats[ext]
    if (!mediaType || mediaType.startsWith('image/')) {
      throw new Error(`Cannot resend ${file.name}: the saved attachment data is unavailable.`)
    }
    const text = file.text ?? ''
    const data = utf8Base64(text)
    return { name: file.name, mediaType, data, size: new TextEncoder().encode(text).length }
  })
}

export function isSupportedClipboardFile(file: File): boolean {
  const ext = file.name.includes('.') ? file.name.split('.').at(-1)?.toLowerCase() ?? '' : ''
  return Boolean(formats[ext] || mimeToExt[file.type])
}

function normalizeAttachmentName(file: File, mediaType: string): string {
  const ext = mimeToExt[mediaType] || 'bin'
  const rawName = file.name.trim()
  if (!rawName || (mediaType.startsWith('image/') && rawName === 'image.png' && file.lastModified)) {
    const suffix = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`
    return mediaType.startsWith('image/') ? `pasted-image-${suffix}.${ext}` : `pasted-text-${suffix}.${ext}`
  }
  if (!rawName.includes('.')) return `${rawName}.${ext}`
  return rawName
}

export function validateAttachments(files: Attachment[], supportsImages: boolean): void {
  if (files.length > 5 || files.reduce((total, file) => total + file.size, 0) > 4 * 1024 * 1024) {
    throw new Error('Attach up to five files, totaling at most 4 MiB.')
  }
  for (const file of files) {
    const image = file.mediaType.startsWith('image/')
    if (image && !supportsImages) throw new Error('The selected model profile does not support image attachments.')
    if (file.size > (image ? 2 * 1024 * 1024 : 128 * 1024)) throw new Error(`${file.name} is too large.`)
  }
}

export async function readAttachment(file: File): Promise<Attachment> {
  const ext = file.name.includes('.') ? file.name.split('.').at(-1)?.toLowerCase() ?? '' : ''
  const mediaType = formats[ext] || (mimeToExt[file.type] ? file.type : '')
  if (!mediaType) throw new Error(`Unsupported file: ${file.name || file.type || 'unknown'}`)
  const name = normalizeAttachmentName(file, mediaType)
  const fileLimit = mediaType.startsWith('image/') ? 2 * 1024 * 1024 : 128 * 1024
  if (file.size > fileLimit) throw new Error(`${name} exceeds the attachment size limit.`)
  if (!mediaType.startsWith('image/')) {
    try {
      const contents = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
      if (contents.includes('\0')) throw new Error('Binary data')
    } catch {
      throw new Error(`${name} is not UTF-8 text.`)
    }
  }
  const data = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read file.'))
    reader.onload = () => resolve(String(reader.result).split(',', 2)[1])
    reader.readAsDataURL(file)
  })
  return { name, mediaType, data, size: file.size }
}

export function downloadMarkdown(text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/markdown;charset=utf-8' }))
  const link = document.createElement('a')
  link.download = 'lune-conversation.md'
  link.href = url
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}
