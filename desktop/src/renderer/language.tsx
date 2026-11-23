import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { Languages } from 'lucide-react'

type Language = 'zh' | 'en'
type SaveLanguage = (language: Language) => Promise<unknown>
const LanguageContext = createContext<{
  language: Language; choose: (language: Language) => void; saveFailed: boolean
  bindHost: (saved: string | undefined, save: SaveLanguage) => void
}>({
  language: 'en', choose: () => undefined, saveFailed: false, bindHost: () => undefined,
})
const preference = 'lune:language'

function initialLanguage(): Language {
  try {
    const value = localStorage.getItem(preference)
    if (value === 'zh' || value === 'en') return value
  } catch { /* browser storage may be disabled */ }
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh' : 'en'
}

export function WebLanguage({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState(initialLanguage)
  const [saveFailed, setSaveFailed] = useState(false)
  const save = useRef<SaveLanguage>(null)
  const pending = useRef(Promise.resolve())
  const bindHost = useCallback((saved: string | undefined, saveLanguage: SaveLanguage) => {
    save.current = saveLanguage
    if (saved === 'zh' || saved === 'en') setLanguage(saved)
  }, [])
  function choose(next: Language) {
    setLanguage(next)
    try { localStorage.setItem(preference, next) } catch { /* keep the current page selection */ }
    const persist = save.current
    if (persist) {
      pending.current = pending.current.then(async () => {
        try { await persist(next); setSaveFailed(false) }
        catch { setSaveFailed(true) }
      })
    }
  }
  useEffect(() => { document.documentElement.lang = language === 'zh' ? 'zh-CN' : 'en' }, [language])
  return <LanguageContext.Provider value={{ language, choose, bindHost, saveFailed }}>{children}</LanguageContext.Provider>
}

export function useHostLanguage() { return useContext(LanguageContext).bindHost }

export function LanguagePicker() {
  const { language, choose, saveFailed } = useContext(LanguageContext)
  const t = useTranslate()
  return <label className="theme-picker web-language" title={t(saveFailed ? 'Could not save interface language.' : 'Interface language')}>
    <Languages size={15} aria-hidden="true" />
    <select aria-label={t('Interface language')} value={language} onChange={event => choose(event.target.value as Language)}>
      <option value="zh" lang="zh-CN">简体中文</option>
      <option value="en" lang="en">English</option>
    </select>
  </label>
}

const chinese: Record<string, string> = {
  'Interface language': '界面语言', 'Could not save interface language.': '无法保存界面语言。',
  'Workspaces': '工作区', 'Open workspace': '打开工作目录',
  'Folder path': '目录路径', 'Parent folder': '上一级', 'Go': '前往', 'Recent folders': '最近打开',
  'No subfolders.': '没有子目录。', 'Opening workspace…': '正在打开工作区…', 'Choose a workspace': '选择工作目录',
  'Files': '文件', 'Close files': '关闭文件面板', 'Show file tree': '显示文件树', 'Hide file tree': '隐藏文件树',
  'Read only': '只读', 'Open file': '打开文件', 'Empty folder.': '空目录。', 'Loading…': '加载中…',
  'Retry': '重试', 'Load more': '加载更多', 'Running': '运行中', 'Needs attention': '待处理', 'Queued': '排队中',
  'Paused': '已暂停', 'Appearance': '外观', 'System': '跟随系统', 'Light': '浅色', 'Dark': '深色',
  'Connected': '已连接', 'Reconnecting': '重新连接中', 'Connecting': '连接中', 'Not connected': '未连接',
  'Connect to this workspace': '连接此工作区', 'Local Core': '本机 Core', 'Remote Core': '远程 Core',
  'Core WebSocket URL': 'Core WebSocket 地址', 'Access token': '访问令牌',
  'Paste your Core token': '粘贴 Core 访问令牌', 'Kept in memory for this page only. The browser connects over TLS.': '令牌只保留在当前页面内存中，浏览器通过 TLS 连接。',
  'Connect to Core': '连接 Core', 'Connect to a Core': '连接 Core', 'Connecting…': '连接中…',
  'Connection settings': '连接设置', 'Disconnect': '断开连接', 'Link a remote Core': '连接远程 Core',
  'The connection dropped. Lune is trying again automatically.': '连接中断，Lune 正在自动重连。',
  'New conversation': '新建会话', 'Conversations': '会话', 'Close conversations': '关闭会话列表',
  'Open conversations': '打开会话列表', 'Conversation': '会话', 'Refresh sessions': '刷新会话',
  'Search conversations': '搜索会话', 'Search titles and messages': '搜索标题和消息',
  'Find a conversation': '查找会话', 'Include archived': '包括已归档', ' (archived)': '（已归档）',
  'Loading conversations…': '正在加载会话…', 'No matching conversations.': '没有匹配的会话。',
  'No conversations yet.': '还没有会话。', 'Loading conversation…': '正在加载会话…',
  'Rename': '重命名', 'Archive': '归档', 'Unarchive': '取消归档', 'Delete…': '删除…',
  'Fork conversation': '分叉会话', 'Export Markdown': '导出 Markdown', 'Compact context': '压缩上下文',
  'Rewind…': '回退…', 'Reasoning effort': '推理强度', 'Default': '默认', 'MODEL': '模型',
  'none': '无', 'minimal': '极低', 'low': '低', 'medium': '中', 'high': '高', 'xhigh': '极高', 'max': '最高',
  'What would you like to work through?': '今天要处理什么？', 'Understand a project': '理解项目',
  'Help me understand this project.': '帮我了解这个项目。', 'Make a plan': '制定计划',
  'Help me make a plan for…': '帮我制定一个计划…', 'YOU': '你', 'Tool calls': '工具调用',
  'Permission needed': '需要授权', 'A question for you': '有一个问题需要你回答', 'Input requested': '需要输入',
  'Tool needs attention': '工具需要处理', 'Run paused': '任务已暂停', 'Execution blocked': '执行受阻',
  'Your response will continue this run.': '回答后将继续执行。', 'Lune wants to use': 'Lune 请求使用',
  'a tool': '一个工具', 'Deny': '拒绝', 'Allow once': '允许本次', 'Allow this session': '本会话允许',
  'You can retry this tool or skip it.': '可重试或跳过此工具。', 'Skip': '跳过', 'Retry tool': '重试工具',
  'This run has a saved checkpoint. Resume it when you are ready.': '任务已有检查点，可继续执行。',
  'This run is paused': '此任务已暂停', 'Resume from its saved checkpoint, or discard it.': '可从检查点恢复或丢弃。',
  'Discard': '丢弃', 'Resume run': '恢复任务', 'Message queue': '消息队列', 'Cancel': '取消',
  'queued': '排队中', 'active': '运行中', 'paused': '已暂停', 'Dismiss': '关闭提示',
  'Reconnecting to Core. Your current work continues there.': '正在重连 Core，任务将继续在 Core 中运行。',
  'Core connection lost.': 'Core 连接已断开。', 'Add a message to the queue…': '将消息加入队列…',
  'Message Lune…': '向 Lune 发送消息…', 'Attach': '添加附件', 'Plan': '计划',
  'Use read-only tools for this turn': '此轮使用只读工具', 'Add to running task': '补充到运行中的任务',
  'Cancel queued': '取消排队', 'Stop': '停止', 'Send message': '发送消息',
  'Session tokens': '会话 Token', 'input': '输入', 'output': '输出', 'context limit': '上下文上限',
  'Rename conversation': '重命名会话', 'Delete conversation?': '删除会话？', 'Rewind conversation': '回退会话',
  'This removes the conversation and its saved history.': '这将删除会话和已保存的历史。',
  'Title': '标题', 'User turns to remove': '移除的用户轮次', 'turn(s) will be removed from the end of the conversation.': '个轮次将从会话末尾移除。',
  'Save': '保存', 'Delete': '删除', 'Rewind': '回退', 'Preview': '预览', 'Close': '关闭',
  'Change the Core that serves this frontend. Your sessions are stored with that Core.': '更换连接的 Core。会话保存在对应的 Core。',
  'The connection was interrupted. Reconnect to continue.': '连接已中断，重新连接后可继续。',
  'Reconnect': '重新连接', 'Connect': '连接', 'Suggested': '建议', 'Your answer': '你的回答',
  'This clarification needs an answer, but Core did not provide question details.': '这个交互需要回答，但 Core 未提供问题内容。',
  'Send answer': '发送回答', 'is asking for input.': '正在请求输入。', 'Response JSON': '响应 JSON',
  'Enter a valid JSON response.': '请输入有效的 JSON 响应。', 'Send response': '发送响应',
  'Remove attachment': '移除附件', 'Open': '打开', 'Connecting to local Core…': '正在连接本机 Core…',
  'Could not open workspace.': '无法打开工作区。', 'Could not browse directories.': '无法浏览目录。',
  'could not open workspace entry': '无法打开工作区条目', 'could not read directory': '无法读取目录',
  'workspace entry not found': '找不到工作区条目', 'invalid workspace path': '无效的工作区路径',
  'unsupported file or directory': '该文件或目录不支持预览', 'could not read file': '无法读取文件',
  'file exceeds the 256 KiB preview limit': '文件超过 256 KiB 的预览上限',
  'only UTF-8 text can be previewed': '仅支持预览 UTF-8 文本',
}

export function useTranslate(): (message: string) => string {
  const { language } = useContext(LanguageContext)
  return language === 'zh' ? translateChinese : identity
}

const identity = (message: string) => message
const translateChinese = (message: string) => chinese[message] ?? message
