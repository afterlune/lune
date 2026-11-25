export interface SlashCommandDef {
  name: string
  aliases?: string[]
  usage: string
  argHint?: string
  description: string
  capability?: string
  category: 'session' | 'execution' | 'harness' | 'ui'
}

export const slashCommands: SlashCommandDef[] = [
  { name: '/new', usage: '/new', description: 'Start a new conversation', category: 'session' },
  { name: '/plan', usage: '/plan [prompt]', argHint: '[prompt]', description: 'Toggle Plan mode or send a prompt in Plan mode', capability: 'turn.plan', category: 'execution' },
  { name: '/files', usage: '/files [path]', argHint: '[path]', description: 'Open workspace file tree or preview a file', capability: 'workspace.files', category: 'harness' },
  { name: '/goal', usage: '/goal [objective|pause|resume|budget N|edit text|clear]', argHint: '[objective | pause | resume | budget N | clear]', description: 'View or manage persistent cross-turn goal', capability: 'goal.manage', category: 'harness' },
  { name: '/ps', usage: '/ps', description: 'List background commands and processes', capability: 'command.control', category: 'harness' },
  { name: '/logs', usage: '/logs <id>', argHint: '<id>', description: 'Inspect output logs of a background command', capability: 'command.control', category: 'harness' },
  { name: '/stop-cmd', usage: '/stop-cmd <id>', argHint: '<id>', description: 'Stop a running background command', capability: 'command.control', category: 'harness' },
  { name: '/mcp', usage: '/mcp [list|enable <name>|disable <name>]', argHint: '[list | enable <name> | disable <name>]', description: 'Inspect or toggle configured MCP servers', capability: 'mcp.inspect', category: 'harness' },
  { name: '/model', usage: '/model [profile]', argHint: '[profile]', description: 'List or switch active model profile', category: 'execution' },
  { name: '/effort', usage: '/effort [level]', argHint: '[none|minimal|low|medium|high|xhigh|max]', description: 'View or set model reasoning effort', category: 'execution' },
  { name: '/permissions', aliases: ['/perm'], usage: '/permissions [read-only|workspace|auto|full-access]', argHint: '[read-only|workspace|auto|full-access]', description: 'View or switch tool permission mode', capability: 'permission.manage', category: 'execution' },
  { name: '/compact', usage: '/compact', description: 'Compact older conversation history', capability: 'session.compact', category: 'execution' },
  { name: '/rewind', usage: '/rewind [N]', argHint: '[N]', description: 'Rewind conversation by N user turns', capability: 'session.rewind', category: 'session' },
  { name: '/fork', usage: '/fork', description: 'Fork current conversation into a new session', capability: 'session.fork', category: 'session' },
  { name: '/title', aliases: ['/rename'], usage: '/title <name>', argHint: '<name>', description: 'Rename the active conversation', category: 'session' },
  { name: '/archive', usage: '/archive', description: 'Archive or unarchive the active conversation', category: 'session' },
  { name: '/drop', aliases: ['/delete'], usage: '/drop', description: 'Delete the active conversation', category: 'session' },
  { name: '/export', usage: '/export', description: 'Export conversation transcript as Markdown', category: 'session' },
  { name: '/stop', usage: '/stop', description: 'Interrupt the active task', category: 'execution' },
  { name: '/cancel-queue', usage: '/cancel-queue', description: 'Cancel queued tasks', capability: 'turn.cancelPending', category: 'execution' },
  { name: '/status', usage: '/status', description: 'Show session, model, and runtime status', category: 'harness' },
  { name: '/context', usage: '/context', description: 'Inspect context window usage and Core capabilities', category: 'harness' },
  { name: '/theme', usage: '/theme [system|light|dark]', argHint: '[system|light|dark]', description: 'Switch interface theme', category: 'ui' },
  { name: '/lang', usage: '/lang [zh|en]', argHint: '[zh|en]', description: 'Switch interface language', category: 'ui' },
  { name: '/help', usage: '/help', description: 'List all available slash commands', category: 'ui' },
]

export function matchSlashCommands(input: string, capabilities: string[]): SlashCommandDef[] {
  if (!input.startsWith('/') || input.includes('\n')) return []
  const trimmed = input.trimStart()
  if (trimmed.includes(' ')) return []
  const query = trimmed.toLowerCase()
  return slashCommands.filter((cmd) => {
    if (cmd.capability && !capabilities.includes(cmd.capability)) return false
    if (query === '/') return true
    if (cmd.name.startsWith(query)) return true
    if (cmd.aliases?.some((alias) => alias.startsWith(query))) return true
    return cmd.description.toLowerCase().includes(query.slice(1))
  })
}

export function parseSlashCommand(input: string): { command: SlashCommandDef; args: string } | null {
  const trimmed = input.trim()
  if (!trimmed.startsWith('/')) return null
  const space = trimmed.search(/\s/)
  const token = (space < 0 ? trimmed : trimmed.slice(0, space)).toLowerCase()
  const args = space < 0 ? '' : trimmed.slice(space).trim()
  const command = slashCommands.find((cmd) => cmd.name === token || cmd.aliases?.includes(token))
  if (!command) return null
  return { command, args }
}
