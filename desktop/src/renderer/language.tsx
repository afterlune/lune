import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, Languages } from 'lucide-react'

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
export function useLanguage() {
  const { language, choose } = useContext(LanguageContext)
  return { language, setLanguage: choose }
}

export function LanguagePicker() {
  const { language, choose, saveFailed } = useContext(LanguageContext)
  const t = useTranslate()
  return <label className="theme-picker web-language" title={t(saveFailed ? 'Could not save interface language.' : 'Interface language')}>
    <Languages size={15} aria-hidden="true" />
    <select aria-label={t('Interface language')} value={language} onChange={event => choose(event.target.value as Language)}>
      <option value="zh" lang="zh-CN">简体中文</option>
      <option value="en" lang="en">English</option>
    </select>
    <ChevronDown size={13} className="select-chevron" aria-hidden="true" />
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
  'Message Lune…': '向 Lune 发送消息…', 'Message Lune, or type / for commands…': '向 Lune 发送消息，或输入 / 调用命令…', 'Attach': '添加附件', 'Plan': '计划',
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
  'Copy': '复制', 'Copied': '已复制',
  'Today': '今天', 'Yesterday': '昨天', 'Previous 7 Days': '过去 7 天', 'Older': '更早',
  'Toggle sidebar': '折叠或展开侧栏', 'Copy message': '复制回复', 'New chat': '新对话',
  'Remember token in this browser': '在此浏览器记住访问令牌',
  'Automatically reconnects on refresh. Uncheck to keep for this tab only.': '刷新页面自动重连；取消勾选则仅在当前标签页有效。',
  'Harness': 'Harness 面板', 'Goal': '目标', 'Processes': '进程', 'Context': '状态',
  'Refresh': '刷新', 'Reference in prompt': '引用到输入框', 'Filter loaded files…': '过滤已加载文件…',
  'Thought process': '思考过程', 'Thinking…': '思考中…', 'Failed': '失败',
  'Proposed plan': '建议计划', 'Implement in this session': '当前上下文执行', 'Implement in new session': '新会话执行',
  'Persistent goal': '跨轮目标', 'Create a persistent objective that guides autonomous execution across turns.': '设置一个持久目标，在多轮执行中持续驱动代理。',
  'Objective': '目标描述', 'Describe the goal to pursue…': '描述要持续推进的目标…', 'Token budget (optional)': 'Token 预算（可选）',
  'Start goal': '启动目标', 'Tokens used': '已用 Token', 'Cached': '缓存命中', 'Pause': '暂停', 'Resume': '继续', 'Edit': '编辑', 'Clear': '清除',
  'Token budget must be a positive integer.': 'Token 预算必须是正整数。',
  'Commands & processes': '后台命令与进程', 'No commands recorded in this session.': '当前会话暂无命令记录。', '(no output yet)': '（暂无输出）',
  'MCP servers': 'MCP 服务器', 'No MCP servers configured in Core.': 'Core 未配置任何 MCP 服务器。',
  'tools': '个工具', 'Disable': '停用', 'Enable': '启用', 'Prompts': '提示模板', 'Resources': '资源',
  'Context & runtime': '上下文与运行态', 'Session ID': '会话 ID', 'Model': '模型', 'Task state': '任务状态',
  'Queued turns': '排队轮次', 'Token usage': 'Token 用量', 'Context window': '上下文窗口',
  'Input tokens': '输入 Token', 'Output tokens': '输出 Token', 'Cached input': '缓存命中', 'Reasoning tokens': '推理 Token',
  'Core capabilities': 'Core 能力列表', 'idle': '空闲', 'running': '运行中', 'completed': '已完成', 'failed': '失败', 'cancelled': '已取消',
  'blocked': '受阻', 'budget_limited': '预算受限', 'complete': '已完成',
  'Session actions': '会话操作', 'Quick actions': '快捷操作', 'Cancel all': '全部取消',
  'Slash commands': '斜杠命令', 'Commands': '命令', 'No matching commands': '无匹配命令',
  'Start a new conversation': '新建会话',
  'Toggle Plan mode (read-only exploration)': '切换 Plan 只读模式',
  'Open workspace file tree or preview a file': '打开文件树或预览指定文件',
  'View or manage persistent cross-turn goal': '查看或管理跨轮持久目标',
  'List background commands and processes': '查看后台命令与进程',
  'Inspect output logs of a background command': '查看后台命令输出日志',
  'Stop a running background command': '停止运行中的后台命令',
  'Inspect or toggle configured MCP servers': '查看或启停 MCP 服务器',
  'List or switch active model profile': '查看或切换当前模型配置',
  'View or set model reasoning effort': '查看或设置模型推理强度',
  'Compact older conversation history': '压缩较早的对话上下文',
  'Rewind conversation by N user turns': '回退最近 N 轮对话',
  'Fork current conversation into a new session': '将当前会话分叉为新会话',
  'Rename the active conversation': '重命名当前会话',
  'Archive or unarchive the active conversation': '归档或取消归档当前会话',
  'Delete the active conversation': '删除当前会话',
  'Export conversation transcript as Markdown': '导出会话为 Markdown',
  'Interrupt the active task': '中断当前任务',
  'Cancel queued tasks': '取消排队中的任务',
  'Show session, model, and runtime status': '显示会话、模型与运行状态',
  'Inspect context window usage and Core capabilities': '查看上下文占用与 Core 能力',
  'Switch interface theme': '切换界面外观主题',
  'Switch interface language': '切换界面语言',
  'List all available slash commands': '列出所有可用斜杠命令',
  'Settings': '设置',
  'Providers': '模型商',
  'Models': '模型',
  'Runtime & Agents': '运行与智能体',
  'Attachment limits': '附件限制',
  'Parallel agents (0 = automatic)': '并行智能体数（0 = 自动）',
  'Context window (0 = automatic)': '上下文窗口（0 = 自动）',
  'Children per agent (0 = default)': '每个智能体的子任务数（0 = 默认）',
  'Delegation depth (0 = default)': '委派深度（0 = 默认）',
  'Automatically compact context': '自动压缩上下文',
  'Compaction threshold (0.1–1.0)': '压缩阈值（0.1–1.0）',
  'Reserve output tokens (0 = default)': '预留输出 Token（0 = 默认）',
  'Maximum files': '最大文件数',
  'Maximum size per file (bytes)': '单个文件最大字节数',
  'Maximum combined attachment size (bytes)': '附件总字节数上限',
  'These limits apply to new tasks. Running tasks keep their current settings.': '这些限制适用于新任务；正在运行的任务继续使用当前配置。',
  'Save runtime settings': '保存运行设置',
  'Runtime settings saved to SQLite database.': '运行设置已保存到 SQLite 数据库。',
  'Settings saved. Reconnect this workspace after the current task finishes to apply the changes.': '设置已保存。当前任务结束后重新连接工作区即可应用。',
  'workspace has running or queued work; retry after it finishes': '工作区仍有运行或排队任务，请在任务结束后重试。',
  'Settings saved and runtime reconnected.': '设置已保存，运行时已重新连接。',
  'Apply settings': '应用设置', 'Applying…': '正在应用…',
  'Interface preferences saved.': '界面偏好已保存。',
  'Message text size': '正文文字大小',
  'Code text size': '代码文字大小',
  'Pressing Enter while a task is running': '任务运行时按 Enter',
  'Queue a follow-up': '将后续消息排队',
  'Interrupt with this message': '用此消息插话',
  'Expand tool and reasoning details by default': '默认展开工具与推理详情',
  'Usage display density': '用量显示密度',
  'Comfortable': '宽松',
  'Compact': '紧凑',
  'Test connection': '测试连接',
  'Testing connection…': '正在测试连接…',
  'Enter a provider ID and Base URL before testing.': '测试前请填写模型商 ID 和 Base URL。',
  'Connection successful': '连接成功',
  'models in': '个模型，用时',
  'Provider connection failed. Check the URL, credentials, and network.': '连接失败，请检查地址、凭据和网络。',
  'The selected environment variable is not set.': '指定的环境变量尚未设置。',
  'Remote & Token': '远程与 Token',
  'General': '通用',
  'Settings sections': '设置分类',
  'Web SQLite DB (settings.db)': 'Web SQLite 库 (settings.db)',
  'Electron SQLite DB (settings.sqlite)': 'Electron SQLite 库 (settings.sqlite)',
  'Browser storage': '浏览器本地存储',
  'Defaults': '默认配置',
  'This workspace': '当前工作区',
  'Import config.json…': '导入 config.json…',
  'Use defaults': '恢复默认',
  'API keys are encrypted and stored in this frontend SQLite database.': 'API Key 已加密保存在当前前端 SQLite 数据库中。',
  'API keys are persisted in this frontend SQLite database (0600); TUI/CLI config.json is untouched.': 'API Key 已持久化保存在当前前端 SQLite 数据库（0600）；TUI/CLI 的 config.json 完全独立不受影响。',
  'No providers configured in this scope yet.': '当前作用域暂未配置模型商。',
  'Key saved in DB': '密钥已存库',
  'No key': '未设密钥',
  'Edit provider': '编辑模型商',
  'Add provider': '添加模型商',
  'New': '新建',
  'Provider ID': '模型商 ID',
  'Protocol': '协议类型',
  'Base URL': '接口 Base URL',
  'Credential source': '凭据来源',
  'API Key (save in SQLite DB)': 'API Key（存入 SQLite 数据库）',
  'Environment variable (apiKeyEnv)': '环境变量（apiKeyEnv）',
  'API key': 'API Key',
  'Leave blank to keep existing DB key': '留空则保持数据库现有密钥',
  'Environment variable name': '环境变量名',
  'Enable server compaction': '启用服务端压缩（serverCompaction）',
  'Remove saved API key': '清除已保存密钥',
  'Update provider': '更新模型商',
  'Save provider': '保存模型商',
  'Advanced JSON editor': '高级 JSON 编辑',
  'Save JSON': '保存 JSON',
  'Enter valid JSON.': '请输入有效的 JSON。',
  'No model profiles configured in this scope yet.': '当前作用域暂未配置模型。',
  'Auto': '自动',
  'Max output': '最大输出',
  'Images': '图片',
  'Set default': '设为默认',
  'Edit model': '编辑模型',
  'Add model': '添加模型',
  'Profile ID': '配置 ID',
  'Provider': '所属模型商',
  'Choose provider…': '选择模型商…',
  'Model ID': '模型 ID',
  'Display name': '显示名称',
  'Context window (0 = auto)': '上下文窗口（0 表示自动）',
  'Max output tokens (0 = auto)': '最大输出 Token（0 表示自动）',
  'Supports image input': '支持图片输入',
  'Supports audio input': '支持音频输入',
  'Set as default model': '设为默认模型',
  'Update model': '更新模型',
  'Save model': '保存模型',
  'Connected to Remote Core': '已连接到远程 Core',
  'Return to local workspaces': '切回本地工作区',
  'No saved remote Core endpoints yet.': '暂未保存远程 Core 连接。',
  'Token saved in DB': 'Token 已存库',
  'Remote Core endpoint': '远程 Core 地址配置',
  'Connection name': '连接名称',
  'Optional label': '可选名称',
  'Save token in SQLite database': '将 Token 保存到 SQLite 数据库',
  'Save only': '仅保存',
  'Save & Connect': '保存并连接',
  'Quick window shortcut': '快捷窗口热键',
  'Model request paused': '模型请求已暂停',
  'The saved task can resume after the retry delay.': '等待重试时间到达后，可以从保存的任务继续。',
  'Attempts': '尝试次数',
  'Retry available in': '可重试倒计时',
  'Retry is available now.': '现在可以重试。',
  'Open model settings': '打开模型设置',
  'Expand conversations': '展开会话栏',
  'Quick navigation': '快捷导航',
  'Resize conversation sidebar': '调整会话侧栏宽度',
  'Resize Harness panel': '调整 Harness 面板宽度',
  'Expand panel': '展开面板',
  'Exit full screen': '退出全屏',
  'rate_limit': '请求频率受限',
  'unavailable': '服务暂不可用',
  'network': '网络错误',
  'timeout': '请求超时',
  'authentication': '身份验证失败',
  'invalid_request': '请求无效',
  'quota': '额度不足',
  'protocol': '协议错误',
  'context_window': '上下文超出限制',
  'Apply': '应用',
  'Provider ID is required.': '请填写模型商 ID。',
  'Environment variable name is required.': '请填写环境变量名。',
  'Model profile ID is required.': '请填写模型配置 ID。',
  'Configure or select a provider first.': '请先配置或选择一个模型商。',
  'Settings saved to SQLite database.': '配置已保存到 SQLite 数据库。',
  'Provider saved to SQLite database.': '模型商已保存到 SQLite 数据库。',
  'Model saved to SQLite database.': '模型已保存到 SQLite 数据库。',
  'Remote connection saved to SQLite database.': '远程连接与 Token 已保存到 SQLite 数据库。',
  'Provider staged. Add at least one model profile and save to persist to SQLite.': '模型商已暂存，请添加一个模型配置后一并保存到 SQLite。',
  'Imported preview ready. Click Save to store into SQLite DB.': '已导入预览，点击保存即可写入 SQLite 数据库。',
  'Workspace now uses the default configuration.': '当前工作区已恢复使用默认配置。',
  'Open folder as file tree': '选择目录打开为文件树',
  'Close workspace': '关闭工作区',
  'Open Settings': '打开设置',
  'Configure models to start': '请先配置模型商与模型',
  'Model providers, API keys, models, and remote connections are stored in your Web SQLite database (TUI/CLI config.json stays separate).': '模型商、API Key、模型与远程连接均独立存储在 Web SQLite 数据库中（与 TUI/CLI 的 config.json 互不影响）。',
  'Select a local project directory to start conversations and inspect files.': '选择一个本地项目目录即可创建独立会话并浏览完整文件树。',
  'Unlock Lune Workspace': '解锁 Lune 工作台',
  'Enter the access token for this Lune host to access workspaces and SQLite settings.': '输入此 Lune 宿主的访问令牌，以访问多工作区与 SQLite 设置。',
  'Invalid access token.': '访问令牌无效。',
  'Fetch models': '拉取模型',
  'Fetching models…': '正在拉取模型列表…',
  'Search fetched models…': '过滤已拉取模型…',
  'No models returned by provider.': '模型商未返回任何模型。',
  'Click a model below to populate Model ID, or enter it manually.': '点击下方从模型商拉取的模型 ID，或直接手动输入。',
  'Available models from provider': '从模型商拉取的可用模型',
  'Default model updated.': '默认模型已更新。',
  'Provider and models saved to SQLite database.': '模型商与模型配置已保存到 SQLite 数据库。',
  'Each model row requires a Model ID and Profile ID.': '每条模型记录都需要填写模型 ID 与配置 ID。',
  'Add at least one model before saving.': '保存前请至少添加一个模型。',
  'Duplicate profile ID': '重复的配置 ID',
  'Presets': '常用预设',
  'Custom endpoint': '自定义端点',
  'Preset providers': '预设模型商',
  'Show key': '显示密钥',
  'Hide key': '隐藏密钥',
  'Enabled models': '已启用模型',
  'Test & fetch models': '测试并拉取模型',
  'Select models to enable': '勾选要启用的模型',
  'Select all': '全选',
  'Apply selection': '应用所选模型',
  'No models enabled yet. Click "Test & fetch models" to multi-select, or "Add model" manually.': '尚未启用模型。点击“测试并拉取模型”批量勾选，或点击“添加模型”手动填写。',
  'Display name (optional)': '显示名称（可选）',
  'Model capacities & options': '模型容量与能力选项',
  'Context window (e.g. 128K, 1M)': '上下文窗口（如 128K、1M）',
  'Max output tokens (e.g. 16K, 128K)': '最大输出 Token（如 16K、128K）',
  'Audio': '音频',
  'Advanced options': '高级选项',
  'Credential configured': '凭据已配置',
  'Credential missing': '缺少凭据',
  'Confirm delete': '确认删除',
  'Add models to this provider': '为此模型商添加模型',
  'Default model': '默认模型',
  'Follow model default': '遵循模型默认设置',
  'Manage models…': '管理模型与配置…',
  'Select model': '选择模型',
  'Configure models': '配置模型',
  'Standard': '标准',
}

export function useTranslate(): (message: string) => string {
  const { language } = useContext(LanguageContext)
  return language === 'zh' ? translateChinese : identity
}

const identity = (message: string) => message
const translateChinese = (message: string) => chinese[message] ?? message
