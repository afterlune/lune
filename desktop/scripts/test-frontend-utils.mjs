import test from 'node:test'
import assert from 'node:assert/strict'

import {
  applyUsageEvent,
  deriveSessionUsageMetrics,
  formatTokenCount,
  mergeAppliedUsageKeys,
} from '../src/renderer/usage-metrics.ts'
import {
  loadLastSessionSettings,
  resolveNewSessionSettings,
  saveLastSessionSettings,
  sessionDefaultsScopeKey,
} from '../src/renderer/session-defaults.ts'
import { deriveAgentSnapshots, parseDelegateTeamHistory } from '../src/renderer/agent-state.ts'
import { restoreHistoryAttachments } from '../src/renderer/attachments.ts'

test('formatTokenCount handles 0, raw integers, K, M boundary, and rounding', () => {
  assert.equal(formatTokenCount(undefined), '0')
  assert.equal(formatTokenCount(null), '0')
  assert.equal(formatTokenCount(0), '0')
  assert.equal(formatTokenCount(-5), '0')
  assert.equal(formatTokenCount(512), '512')
  assert.equal(formatTokenCount(999), '999')
  assert.equal(formatTokenCount(1_000), '1K')
  assert.equal(formatTokenCount(1_240), '1.2K')
  assert.equal(formatTokenCount(18_500), '18.5K')
  assert.equal(formatTokenCount(200_000), '200K')
  assert.equal(formatTokenCount(999_949), '999.9K')
  assert.equal(formatTokenCount(999_950), '1M')
  assert.equal(formatTokenCount(1_000_000), '1M')
  assert.equal(formatTokenCount(1_250_000), '1.3M')
})

test('applyUsageEvent + deriveSessionUsageMetrics tracks totals, subagents, lead context, and compaction', () => {
  const baseSession = {
    id: 's1',
    updatedAt: '2026-11-25T00:00:00Z',
    title: 'Test',
    archived: false,
    settings: { modelProfile: 'alpha', reasoningEffort: '' },
    sessionUsage: {
      requests: 2,
      measured: 2,
      input: 100_000,
      output: 4_000,
      reasoning: 0,
      total: 104_000,
      cached: 50_000,
    },
    usageRequests: [
      {
        request: 1,
        request_id: 'r1',
        available: true,
        agent_id: 'lead',
        purpose: 'main',
        usage: { input_tokens: 40_000, output_tokens: 2_000, total_tokens: 42_000 },
        context_window: 200_000,
      },
      {
        request: 2,
        request_id: 'r2',
        available: true,
        agent_id: 'worker-1',
        purpose: 'team',
        usage: { input_tokens: 60_000, output_tokens: 2_000, total_tokens: 62_000 },
      },
    ],
  }

  const runtime = { task: { status: 'running' }, queue: [] }
  const initial = deriveSessionUsageMetrics({ session: baseSession, runtime })
  assert.equal(initial.usedContext, 40_000)
  assert.equal(initial.contextWindow, 200_000)
  assert.equal(initial.contextPct, 20)
  assert.equal(initial.cachePct, 50)
  assert.equal(initial.total, 104_000)

  const afterSub = applyUsageEvent(baseSession, {
    kind: 'usage',
    cursor: 10,
    requestId: 'r3',
    agentId: 'worker-2',
    usagePurpose: 'team',
    usage: {
      input_tokens: 90_000,
      input_tokens_details: { cached_tokens: 80_000 },
      output_tokens: 1_000,
      total_tokens: 91_000,
    },
  })
  const subMetrics = deriveSessionUsageMetrics({ session: afterSub, runtime })
  assert.equal(subMetrics.usedContext, 40_000)
  assert.equal(subMetrics.input, 190_000)
  assert.equal(subMetrics.cached, 130_000)
  assert.equal(subMetrics.total, 195_000)

  const afterLead = applyUsageEvent(afterSub, {
    kind: 'usage',
    cursor: 11,
    requestId: 'r4',
    agentId: 'lead',
    usagePurpose: 'main',
    contextWindow: 200_000,
    provider: 'openai',
    modelProfile: 'alpha',
    model: 'alpha-v1',
    usage: {
      input_tokens: 82_000,
      input_tokens_details: { cached_tokens: 60_000 },
      output_tokens: 2_000,
      total_tokens: 84_000,
    },
  })
  const leadMetrics = deriveSessionUsageMetrics({ session: afterLead, runtime })
  assert.equal(leadMetrics.usedContext, 82_000)
  assert.equal(leadMetrics.contextPct, 41)
  assert.equal(afterLead.usageRequests.at(-1)?.model, 'alpha-v1')

  const compacted = deriveSessionUsageMetrics({
    session: afterLead,
    runtime,
    liveContext: { compacting: false, afterTokens: 24_000 },
  })
  assert.equal(compacted.usedContext, 24_000)
  assert.equal(compacted.contextPct, 12)

  const mergedKeys = mergeAppliedUsageKeys(new Set(['cursor:99', 'req:old:0::']), afterLead)
  assert.equal(mergedKeys.has('cursor:99'), true)
  assert.equal(mergedKeys.has('req:r4:0:lead:main'), true)
  assert.equal(mergedKeys.has('req:old:0::'), false)
})

test('session-defaults handles scoped memory, explicit Auto override, and deleted model fallback', () => {
  const scopeA = sessionDefaultsScopeKey({ mode: 'local', workspace: '/tmp/ws-a' })
  const scopeB = sessionDefaultsScopeKey({ mode: 'local', workspace: '/tmp/ws-b' })
  assert.equal(scopeA, 'local:/tmp/ws-a')

  saveLastSessionSettings(scopeA, { modelProfile: 'beta', reasoningEffort: 'high' })
  const loadedA = loadLastSessionSettings(scopeA)
  assert.equal(loadedA?.modelProfile, 'beta')
  assert.equal(loadedA?.reasoningEffort, 'high')

  // Switching to explicit Auto ('') must override previous 'high'
  saveLastSessionSettings(scopeA, { reasoningEffort: '' })
  const loadedAuto = loadLastSessionSettings(scopeA)
  assert.equal(loadedAuto?.modelProfile, 'beta')
  assert.equal(loadedAuto?.reasoningEffort, '')

  // Global fallback works for new scopes
  const loadedB = loadLastSessionSettings(scopeB)
  assert.equal(loadedB?.modelProfile, 'beta')
  assert.equal(loadedB?.reasoningEffort, '')

  // Deleted model falls back to Core default while keeping valid reasoningEffort
  saveLastSessionSettings(scopeA, { modelProfile: 'deleted-profile', reasoningEffort: 'max' })
  const created = {
    id: 'new-1',
    updatedAt: '2026-11-25T00:00:00Z',
    title: '',
    archived: false,
    settings: { modelProfile: 'alpha', reasoningEffort: '' },
  }
  const models = [
    { profile: 'alpha', provider: 'openai', model: 'alpha-v1' },
    { profile: 'beta', provider: 'anthropic', model: 'beta-v2' },
  ]
  const resolved = resolveNewSessionSettings(created, loadLastSessionSettings(scopeA), models)
  assert.equal(resolved.changed, true)
  assert.equal(resolved.nextSettings.modelProfile, 'alpha')
  assert.equal(resolved.nextSettings.reasoningEffort, 'max')
})

test('parseDelegateTeamHistory & deriveAgentSnapshots aggregate live + historical subagents and usage', () => {
  const timeline = [
    { kind: 'message', id: 'u1', role: 'user', text: 'Inspect codebase in parallel' },
    {
      kind: 'tool_round',
      id: 't1',
      calls: [
        {
          callId: 'c1',
          name: 'delegate_team',
          arguments: JSON.stringify({
            tasks: [
              { id: 'research', model: 'flash', task: 'Read backend handlers' },
              { id: 'review', model: 'pro', task: 'Check permission checks' },
            ],
          }),
          output: [
            'Team results:',
            '',
            '## research (model flash)',
            'Context: task-only',
            'Found 3 HTTP routes.',
            '',
            '## review (model pro)',
            'Context: forked',
            'Failed: request timed out',
            'Delegation remaining for this agent: 2; maximum depth: 2.',
          ].join('\n'),
        },
      ],
    },
  ]

  const parsed = parseDelegateTeamHistory(timeline)
  assert.equal(parsed.length, 2)
  assert.equal(parsed[0].id, 'lead/research')
  assert.equal(parsed[0].status, 'completed')
  assert.equal(parsed[0].contextMode, 'task-only')
  assert.equal(parsed[0].outputText, 'Found 3 HTTP routes.')
  assert.equal(parsed[1].id, 'lead/review')
  assert.equal(parsed[1].status, 'failed')
  assert.equal(parsed[1].error, 'request timed out')

  let session = {
    id: 'sess-a',
    updatedAt: '2026-11-25T00:00:00Z',
    title: 'Agents test',
    archived: false,
    settings: { modelProfile: 'pro' },
    sessionUsage: { requests: 1, measured: 1, input: 10_000, output: 500, reasoning: 0, total: 10_500, cached: 5_000 },
    agentUsage: {
      lead: { requests: 1, measured: 1, input: 10_000, output: 500, reasoning: 0, total: 10_500, cached: 5_000 },
    },
    usageRequests: [
      {
        request: 1,
        request_id: 'lead-1',
        available: true,
        agent_id: 'lead',
        purpose: 'main',
        model_profile: 'pro',
        context_window: 200_000,
        usage: { input_tokens: 10_000, input_tokens_details: { cached_tokens: 5_000 }, output_tokens: 500, total_tokens: 10_500 },
      },
    ],
  }

  session = applyUsageEvent(session, {
    kind: 'usage',
    cursor: 20,
    requestId: 'sub-1',
    agentId: 'lead/research',
    usagePurpose: 'team',
    modelProfile: 'flash',
    contextWindow: 128_000,
    usage: { input_tokens: 32_000, input_tokens_details: { cached_tokens: 16_000 }, output_tokens: 1_200, total_tokens: 33_200 },
  })

  const snaps = deriveAgentSnapshots({
    session,
    timeline,
    liveAgents: [
      {
        id: 'lead/research',
        name: 'research',
        role: 'worker',
        task: 'Read backend handlers',
        status: 'running',
        activity: 'Generating response',
        toolCount: 2,
        tools: [{ id: 'tool-1', name: 'read_file', status: 'completed', durationMs: 42 }],
        startedAt: Date.now() - 1000,
        outputText: 'Live partial answer',
        tokensPerSecond: 45.2,
      },
    ],
    runtime: { task: { status: 'running', modelProfile: 'pro', contextWindow: 200_000 }, queue: [] },
    models: [
      { profile: 'pro', provider: 'deepseek', model: 'deepseek-pro', contextWindow: 200_000 },
      { profile: 'flash', provider: 'deepseek', model: 'deepseek-flash', contextWindow: 128_000 },
    ],
    taskIsRunning: true,
  })

  assert.equal(snaps.length, 3)
  assert.equal(snaps[0].isLead, true)
  assert.equal(snaps[0].totalTokens, 10_500)
  assert.equal(snaps[1].id, 'lead/research')
  assert.equal(snaps[1].status, 'running')
  assert.equal(snaps[1].usedContext, 32_000)
  assert.equal(snaps[1].contextPct, 25)
  assert.equal(snaps[1].outputText, 'Live partial answer')
  assert.equal(snaps[1].toolCount, 2)
  assert.equal(snaps[2].id, 'lead/review')
  assert.equal(snaps[2].status, 'failed')
})

test('restoreHistoryAttachments preserves image bytes and UTF-8 text content', () => {
  const restored = restoreHistoryAttachments([
    { name: 'Image', image: 'data:image/png;base64,iVBORw0KGgo=' },
    { name: 'notes.txt', text: '你好, Lune' },
  ])
  assert.deepEqual(restored[0], {
    name: 'Image.png',
    mediaType: 'image/png',
    data: 'iVBORw0KGgo=',
    size: 8,
  })
  assert.equal(restored[1].name, 'notes.txt')
  assert.equal(restored[1].mediaType, 'text/plain')
  assert.equal(Buffer.from(restored[1].data, 'base64').toString('utf8'), '你好, Lune')
  assert.throws(() => restoreHistoryAttachments([{ name: 'archive.zip' }]), /saved attachment data is unavailable/)
})
