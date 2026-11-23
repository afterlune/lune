export type Attachment = { name: string; mediaType: string; data: string; size: number }

const formats: Record<string, string> = {
  txt: 'text/plain', md: 'text/markdown', markdown: 'text/markdown', csv: 'text/csv',
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg',
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
  const mediaType = formats[file.name.split('.').at(-1)?.toLowerCase() ?? '']
    || (file.type === 'text/plain' && !file.name.includes('.') ? file.type : '')
  if (!mediaType) throw new Error(`Unsupported file: ${file.name}`)
  const fileLimit = mediaType.startsWith('image/') ? 2 * 1024 * 1024 : 128 * 1024
  if (file.size > fileLimit) throw new Error(`${file.name} exceeds the attachment size limit.`)
  if (!mediaType.startsWith('image/')) {
    try {
      const contents = new TextDecoder('utf-8', { fatal: true }).decode(await file.arrayBuffer())
      if (contents.includes('\0')) throw new Error('Binary data')
    } catch {
      throw new Error(`${file.name} is not UTF-8 text.`)
    }
  }
  const data = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error ?? new Error('Unable to read file.'))
    reader.onload = () => resolve(String(reader.result).split(',', 2)[1])
    reader.readAsDataURL(file)
  })
  return { name: file.name, mediaType, data, size: file.size }
}

export function downloadMarkdown(text: string): void {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/markdown;charset=utf-8' }))
  const link = document.createElement('a')
  link.download = 'lune-conversation.md'
  link.href = url
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 60_000)
}
