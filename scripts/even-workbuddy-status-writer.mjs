#!/usr/bin/env node
// WorkBuddy (CodeBuddy Code) hook -> Even G2 status bridge.
//
// WorkBuddy runs hooks on the agent lifecycle. This script receives the hook
// payload on stdin and turns it into three artifacts under
// ~/.workbuddy/statusbar/ that the Even Control desktop app mirrors onto the
// glasses:
//
//   state.d/<sessionId>.json   current state + roll-up (overwritten, cheap head)
//   stream.d/<sessionId>.jsonl append-only step stream (what the desktop shows)
//   files.d/<sessionId>.json   ledger of touched files (path / action / +-)
//
// Why three files: the old adapter only stored a state enum, so the glasses
// could show "正在执行工具" and nothing else. The desktop actually renders a
// *conversation stream* — what the user asked, what the agent is doing, which
// tool touched which file, and what changed. stream.d is that stream; files.d
// is the "改了什么" ledger; state.d stays the cheap summary the monitor polls.
//
// Sources of truth, in order of trust:
//   1. the hook payload itself  -> deterministic, never races (tool + input)
//   2. transcript_path (JSONL)  -> tailed incrementally for the two things the
//      hook contract does not carry: assistant narration text and WorkBuddy's
//      own argumentsDisplayText rendering. Only new bytes are read, so the
//      cost does not grow with session length.
//
// Never blocks the agent: any failure is swallowed on purpose.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// Tools that mean "the agent is blocked on the human".
const NEEDS_INPUT_TOOLS = new Set(['AskUserQuestion'])

const TOOL_ALIASES = {
  request_user_input: 'AskUserQuestion',
  exec_command: 'Bash',
  'functions.exec_command': 'Bash',
  apply_patch: 'Edit',
  'functions.apply_patch': 'Edit',
  web__run: 'WebSearch',
  'web.run': 'WebSearch',
  spawn_agent: 'Task',
  'collaboration.spawn_agent': 'Task',
}

function normalizeToolName(rawTool) {
  const shortTool = rawTool.split('.').at(-1)
  const alias = TOOL_ALIASES[rawTool] || TOOL_ALIASES[shortTool]
  if (alias) return alias

  // Codex hook versions have emitted the same web tool as web.run,
  // web__run and webrun. Match the semantic name so an internal transport
  // spelling never leaks into the glasses UI.
  const compactTool = rawTool.replace(/[^a-z0-9]/gi, '').toLowerCase()
  if (compactTool === 'webrun') return 'WebSearch'
  return rawTool
}

// Tools that mutate a file, mapped to their ledger action.
const FILE_TOOLS = {
  Write: 'create',
  Edit: 'modify',
  MultiEdit: 'modify',
  NotebookEdit: 'modify',
}

// EVEN_STATUS_ROOT lets the test harness redirect every artifact to a temp
// directory. Production always uses ~/.workbuddy/statusbar.
const AGENT = process.env.EVEN_AGENT === 'codex' ? 'codex' : 'workbuddy'
const STATUS_ROOT = process.env.EVEN_STATUS_ROOT
  ? path.resolve(process.env.EVEN_STATUS_ROOT)
  : path.join(os.homedir(), `.${AGENT}`, 'statusbar')
const STATE_DIR = path.join(STATUS_ROOT, 'state.d')
const STREAM_DIR = path.join(STATUS_ROOT, 'stream.d')
const FILES_DIR = path.join(STATUS_ROOT, 'files.d')
const OFFSET_DIR = path.join(STATUS_ROOT, '.transcript-offsets')

// The stream is append-only; older lines are compacted away so a long session
// cannot grow the file without bound. The desktop only ever reads the tail.
const STREAM_LIMIT = 600

// Debug helper: when ~/.workbuddy/statusbar/.capture exists, every raw hook
// payload is appended to capture.jsonl. Used to inspect the real shape of
// tool_input / tool_response without guessing. Remove the flag when done.
const CAPTURE_FLAG = path.join(STATUS_ROOT, '.capture')
const CAPTURE_FILE = path.join(STATUS_ROOT, 'capture.jsonl')

let raw = ''
let flushed = false

process.stdin.setEncoding('utf8')
process.stdin.on('data', chunk => { raw += chunk })
process.stdin.on('end', flush)
process.stdin.on('error', flush)
// Safety net: the harness may not close stdin for very fast hooks.
setTimeout(flush, 1000).unref()

function flush() {
  if (flushed) return
  flushed = true

  let payload = {}
  try { payload = JSON.parse(raw || '{}') } catch {}

  capture(payload)

  try {
    handle(payload)
  } catch {
    // Status reporting must never break the session.
  }
}

function handle(payload) {
  payload = normalizePayload(payload)
  const event = String(payload.hook_event_name || process.argv[2] || '')
  const sessionId = String(payload.session_id || '').replace(/[^A-Za-z0-9_.-]/g, '').slice(0, 80)
  const state = resolveState(event, payload)
  if (!sessionId || !state) return

  const project = path.basename(String(payload.cwd || ''))
  const previous = readState(sessionId)
  const now = Date.now() / 1000

  // A submitted prompt starts a new user-visible turn. The desktop task may
  // live for hours, but the lens timer, changes and recent activity describe
  // only the work triggered by this prompt.
  if (event === 'UserPromptSubmit') resetTurn(sessionId)

  // The live "what is it doing right now" line. Settled steps go to the stream;
  // the in-flight one is carried by the header so a slow tool still shows up.
  const current = describeToolCall(payload)

  const settled = collectSettledSteps(event, payload, project)
  if (settled.length > 0) appendStream(sessionId, settled)

  const ledger = updateLedger(sessionId, project, event, payload)

  const record = {
    state,
    sessionId,
    project,
    source: AGENT,
    hookEvent: event,
    ts: now,
    // --- roll-up the glasses header and desktop panel use ---
    stepCount: event === 'UserPromptSubmit' ? settled.length : (previous.stepCount || 0) + settled.length,
    changeCount: event === 'UserPromptSubmit' ? 0 : (previous.changeCount || 0) + successfulChangeCount(event, payload),
    startedAt: event === 'UserPromptSubmit' ? now : previous.startedAt || now,
		title: readTitle(sessionId) || (event === 'UserPromptSubmit' ? promptTitle(payload.prompt) : '') || previous.title || '',
    current: event === 'PreToolUse' || event === 'PermissionRequest' ? current.text : '',
    currentTool: event === 'PreToolUse' ? current.tool : '',
    fileCount: ledger.fileCount,
    additions: ledger.additions,
    deletions: ledger.deletions,
  }
  if (state === 'done') record.current = ''

  writeState(sessionId, record)
}

function successfulChangeCount(event, payload) {
  return event === 'PostToolUse' && !isFailed(payload) && fileChange(payload) ? 1 : 0
}

function resetTurn(sessionId) {
  for (const file of [
    path.join(STREAM_DIR, `${sessionId}.jsonl`),
    path.join(FILES_DIR, `${sessionId}.json`),
  ]) {
    try { fs.unlinkSync(file) } catch {}
  }
}

// Codex and WorkBuddy expose the same lifecycle concepts with slightly
// different casing and field names. Normalize once so both agents share the
// same stream, ledger and rendering contract.
function normalizePayload(payload) {
  const rawTool = String(payload.tool_name || payload.toolName || payload.tool || '')
  const toolName = normalizeToolName(rawTool)
  const rawInput = payload.tool_input ?? payload.toolInput ?? payload.input ?? payload.arguments ?? {}
  let toolInput = rawInput && typeof rawInput === 'object'
    ? rawInput
    : toolName === 'Edit' && typeof rawInput === 'string'
      ? {patch: rawInput}
      : {}
  if (toolName === 'Edit' && typeof toolInput.command === 'string' && toolInput.command.includes('*** Begin Patch')) {
    toolInput = {...toolInput, patch: toolInput.command}
  }
  return {
    ...payload,
    hook_event_name: payload.hook_event_name || payload.hookEventName || process.argv[2] || '',
    session_id: payload.session_id || payload.sessionId || '',
    cwd: payload.cwd || payload.workdir || payload.working_directory || '',
    tool_name: toolName,
    tool_input: toolInput,
    tool_response: payload.tool_response ?? payload.toolResponse ?? payload.response ?? payload.output,
    transcript_path: payload.transcript_path || payload.transcriptPath || '',
    prompt: payload.prompt || payload.user_prompt || payload.userPrompt || payload.message || '',
  }
}

// resolveState maps a hook event to the coarse state the desktop shows.
function resolveState(event, payload) {
  const tool = String(payload.tool_name || '')
  switch (event) {
    case 'SessionStart':
      return 'idle'
    case 'UserPromptSubmit':
      return 'thinking'
    case 'PreToolUse':
      return NEEDS_INPUT_TOOLS.has(tool) ? 'needs_input' : 'tool'
    case 'PostToolUse':
    case 'PostToolUseFailure':
    case 'PostCompact':
    case 'SubagentStop':
    case 'TaskCompleted':
    case 'PermissionDenied':
      return 'thinking'
    case 'Notification':
      if (isPausedNotification(payload)) return 'paused'
      // "permission_prompt" = 需要授权；其余（如 60s 空闲提醒）= 在等你。
      return payload.notification_type === 'permission_prompt' ? 'permission' : 'needs_input'
    case 'PermissionRequest':
      return 'permission'
    case 'Elicitation':
      return 'needs_input'
    case 'PreCompact':
      return 'compacting'
    case 'SubagentStart':
    case 'TaskCreated':
      return 'tool'
    case 'TeammateIdle':
      return 'idle'
    case 'Stop':
    case 'SessionEnd':
      return 'done'
    case 'StopFailure':
      return 'paused'
    default:
      return ''
  }
}

function isPausedNotification(payload) {
  const type = String(payload.notification_type || payload.notificationType || '').toLowerCase()
  const message = String(payload.message || '').toLowerCase()
  return type.includes('pause') || type.includes('interrupt') || type.includes('abort') ||
    message.includes('paused') || message.includes('interrupted') || message.includes('aborted') ||
    message.includes('已暂停') || message.includes('已中断')
}

// collectSettledSteps returns the stream entries this event closes out.
//
// Only *finished* work is appended: a PreToolUse is not a step yet, it is the
// live indicator. This keeps the stream a log of what actually happened
// instead of a log of intentions, which halves the row churn on the glasses.
function collectSettledSteps(event, payload, project) {
  const steps = []
  const ts = Date.now() / 1000

  if (event === 'SessionStart') {
    steps.push({ ts, kind: 'note', text: project ? `会话开始 · ${project}` : '会话开始' })
  }

  if (event === 'UserPromptSubmit') {
    const prompt = firstLine(payload.prompt, 120)
    if (prompt) steps.push({ ts, kind: 'prompt', text: prompt })
  }

  if (event === 'PreCompact') {
    steps.push({ ts, kind: 'note', text: payload.trigger === 'manual' ? '手动压缩上下文' : '自动压缩上下文' })
  }

  if (event === 'PostToolUse' || event === 'PostToolUseFailure') {
    const call = describeToolCall(payload)
    const failed = event === 'PostToolUseFailure' || isFailed(payload)
    const step = {
      ts,
      kind: 'tool',
      status: failed ? 'fail' : 'ok',
      tool: call.tool,
      text: call.text,
      callId: String(payload.call_id || payload.tool_use_id || ''),
    }
    const file = fileChange(payload)
    if (file && !failed) {
      step.file = file.basename
      step.path = file.path
      step.action = file.action
      step.add = file.additions
      step.del = file.deletions
    }
    steps.push(step)
  }

  if (event === 'PermissionRequest' || event === 'Notification') {
    const type = String(payload.notification_type || '')
    const call = describeToolCall(payload)
    // A PermissionRequest carries no notification_type, so the event name is
    // part of the test too.
    const asking = event === 'PermissionRequest' || type === 'permission_prompt'
    // The row records the fact (which tool, which target); the wording belongs
    // to the view, whose "?" gutter already says "waiting on you". The harness'
    // own prose ("needs your permission to use Bash") is long, English, and
    // would be clipped anyway.
    const text = asking
      ? (call.text || '需要你的授权')
      : firstLine(payload.message, 100) || (type === 'idle_prompt' ? '已空闲，等你回来' : '')
    if (text) steps.push({ ts, kind: 'ask', tool: asking ? call.tool : '', text })
  }

  if (event === 'Elicitation') {
    steps.push({ ts, kind: 'ask', text: firstLine(payload.message, 100) || '等待你的回答' })
  }

  if (event === 'StopFailure') {
    steps.push({ ts, kind: 'note', text: '任务已暂停' })
  }

  if (event === 'TaskCreated' || event === 'TaskCompleted') {
    // Only a real subject says anything; the numeric task id on its own renders
    // as "完成任务 · 2", which is noise on a lens row.
    const subject = firstLine(payload.subject || payload.description, 80)
    if (subject) {
      steps.push({ ts, kind: 'note', text: `${event === 'TaskCreated' ? '新增任务' : '完成任务'} · ${subject}` })
    }
  }

  // Assistant narration is picked up from the transcript: it lags the hook by
  // one event, so it lands on the next flush. Stop is the last flush of a turn,
  // which is why the closing summary arrives at the right moment.
  if (event === 'SessionStart' || event === 'PostToolUse' || event === 'PostToolUseFailure' ||
      event === 'PostCompact' || event === 'TaskCompleted' || event === 'SubagentStop' ||
      event === 'Stop' || event === 'StopFailure' || event === 'SessionEnd') {
    steps.push(...readTranscriptTail(payload))
  }

  if (event === 'Stop' || event === 'SessionEnd') {
    steps.push({ ts, kind: 'done', text: '本轮结束' })
  }

  return steps
}

// --- transcript -----------------------------------------------------------------

// readTranscriptTail consumes only the bytes appended since the last read and
// returns the assistant narration they contain. Tool records are deliberately
// skipped: the hook already reports those, and reading them twice would
// duplicate every step.
function readTranscriptTail(payload) {
  const file = String(payload.transcript_path || '')
  const sessionId = String(payload.session_id || '').replace(/[^A-Za-z0-9_.-]/g, '').slice(0, 80)
  if (!file || !sessionId || !fs.existsSync(file)) return []

  const offsetFile = path.join(OFFSET_DIR, sessionId)
  let offset = 0
  try { offset = Number(fs.readFileSync(offsetFile, 'utf8')) || 0 } catch {}
  const size = fs.statSync(file).size
  if (size <= offset) {
    if (size < offset) writeOffset(offsetFile, size) // transcript was replaced
    return []
  }

  let text = ''
  try {
    const fd = fs.openSync(file, 'r')
    try {
      const length = size - offset
      const buffer = Buffer.alloc(length)
      const read = fs.readSync(fd, buffer, 0, length, offset)
      text = buffer.subarray(0, read).toString('utf8')
    } finally {
      fs.closeSync(fd)
    }
  } catch {
    return []
  }

  // Never consume a partially written trailing line: rewind to the last \n.
  const lastBreak = text.lastIndexOf('\n')
  if (lastBreak < 0) return []
  const complete = text.slice(0, lastBreak + 1)
  writeOffset(offsetFile, offset + Buffer.byteLength(complete, 'utf8'))

  const steps = []
  for (const line of complete.split('\n')) {
    if (!line.trim()) continue
    let record
    try { record = JSON.parse(line) } catch { continue }
    if (record.type === 'ai-title' && record.aiTitle) {
      rememberTitle(sessionId, firstLine(record.aiTitle, 40))
      continue
    }
    if (record.type === 'message' && record.role === 'assistant' && Array.isArray(record.content)) {
      const said = record.content
        .filter(block => block && block.type === 'output_text')
        .map(block => firstLine(block.text, 200))
        .filter(Boolean)
        .join(' ')
      if (said) steps.push({ ts: (Number(record.timestamp) || Date.now()) / 1000, kind: 'say', text: said })
    }
  }
  return steps
}

// readTitle pulls the conversation title WorkBuddy generates, for the header.
function readTitle(sessionId) {
  const file = path.join(STATE_DIR, `${sessionId}.title`)
  try {
    return fs.readFileSync(file, 'utf8').trim()
  } catch {
    return ''
  }
}

function rememberTitle(sessionId, title) {
  if (!sessionId || !title) return
  try {
    // SessionStart can be the very first event of a session, before state.d
    // exists — creating it here is what keeps the title from being dropped.
    fs.mkdirSync(STATE_DIR, {recursive: true})
    fs.writeFileSync(path.join(STATE_DIR, `${sessionId}.title`), title)
  } catch {
    // Best effort.
  }
}

function writeOffset(file, value) {
  try {
    fs.mkdirSync(path.dirname(file), { recursive: true })
    fs.writeFileSync(file, String(value))
  } catch {
    // Best effort.
  }
}

// --- steps ----------------------------------------------------------------------

// describeToolCall mirrors WorkBuddy's own argumentsDisplayText: a short, human
// label for one tool invocation ("statusmonitor.go", or the shell command).
function describeToolCall(payload) {
  const tool = String(payload.tool_name || '')
  const input = payload.tool_input && typeof payload.tool_input === 'object' ? payload.tool_input : {}
  if (!tool) return { tool: '', text: '' }

  const base = value => (value ? path.basename(String(value)) : '')
  const clip = (value, limit) => {
    const flat = String(value || '').replace(/\s+/g, ' ').trim()
    return flat.length > limit ? `${flat.slice(0, limit - 1)}…` : flat
  }

  switch (tool) {
    case 'Read':
    case 'Write':
    case 'Edit':
    case 'MultiEdit':
    case 'NotebookEdit':
      return { tool, text: base(input.file_path || input.path || (typeof input.patch === 'string' ? input.patch.match(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/m)?.[1] : '')) }
    case 'Bash':
      return { tool, text: clip(summarizeCommand(input.command || input.cmd || input.code), 80) }
    case 'Grep':
      return { tool, text: clip(input.pattern, 40) }
    case 'Glob':
      return { tool, text: clip(input.pattern, 40) }
    case 'WebFetch':
      return { tool, text: clip(input.url, 60) }
    case 'WebSearch':
      return { tool, text: clip(input.query || input.search_query?.[0]?.q, 50) }
    case 'Task':
    case 'Agent':
      return { tool: 'Task', text: clip(input.description || input.prompt, 50) }
    case 'TaskCreate':
      return { tool, text: clip(input.subject, 50) }
    case 'TaskUpdate':
      return { tool, text: clip(input.subject || (input.taskId ? `#${input.taskId}` : ''), 50) }
    case 'TaskList':
    case 'TaskGet':
    case 'TaskStop':
      return { tool, text: '' }
    case 'TodoWrite':
      return { tool, text: '更新任务清单' }
    case 'Skill':
      return { tool, text: clip(input.skill || input.command, 40) }
    default:
      return { tool, text: clip(input.description, 60) }
  }
}

// summarizeCommand strips the part of a shell command that carries no meaning
// on a one-line lens row.
//
//   cd /Users/me/project && go test ./...   -> go test ./...
//   /opt/homebrew/bin/node scripts/x.mjs    -> scripts/x.mjs
//
// Without this, the clipped row shows an absolute path and the actual command
// never fits.
const INTERPRETERS = new Set(['node', 'python', 'python3', 'sh', 'bash', 'zsh', 'ruby', 'deno', 'bun', 'npx', 'pnpx'])

function summarizeCommand(command) {
  let text = String(command || '').replace(/\s+/g, ' ').trim()
  text = text.replace(/^cd\s+(?:"[^"]*"|'[^']*'|[^&|;]+?)\s*(?:&&|;)\s*/, '').trim()

  // Drop a leading interpreter, whether it was invoked bare ("node") or through
  // an absolute path ("/…/versions/22/bin/node").
  const [head, ...rest] = text.split(' ')
  if (rest.length > 0 && INTERPRETERS.has(path.basename(head))) {
    text = rest.join(' ')
  }
  return collapseHome(text).trim()
}

// collapseHome shortens the user's home directory to ~, which is the difference
// between "~/…/node" fitting on the row and not.
function collapseHome(text) {
  const home = os.homedir()
  return home && text.includes(home) ? text.split(home).join('~') : text
}

// fileChange measures the edit a tool call produced. The numbers come from the
// tool input (line counts) so they are available live, per step — the per-turn
// totals in ~/.workbuddy/changes-index are the authoritative counterpart.
function fileChange(payload) {
  const tool = String(payload.tool_name || '')
  let action = FILE_TOOLS[tool]
  if (!action) return null
  const input = payload.tool_input && typeof payload.tool_input === 'object' ? payload.tool_input : {}
  const patchPath = typeof input.patch === 'string'
    ? input.patch.match(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/m)?.[1]
    : ''
  const filePath = String(input.file_path || input.path || patchPath || '')
  if (!filePath) return null

  // Write covers both "create" and "overwrite". The tool response says which
  // ("...wrote to new file: <path>"); without that hint, assume a create, which
  // is what Write usually means.
  if (tool === 'Write') {
    const response = typeof payload.tool_response === 'string' ? payload.tool_response : ''
    action = response === '' || /new file/i.test(response) ? 'create' : 'modify'
  }

  const lines = value => (typeof value === 'string' && value.length > 0 ? value.split('\n').length : 0)
  const additions = input.patch
    ? String(input.patch).split('\n').filter(line => line.startsWith('+') && !line.startsWith('+++')).length
    : tool === 'Edit' || tool === 'MultiEdit'
      ? lines(input.new_string)
      : lines(input.content)
  const deletions = input.patch
    ? String(input.patch).split('\n').filter(line => line.startsWith('-') && !line.startsWith('---')).length
    : tool === 'Edit' || tool === 'MultiEdit'
      ? lines(input.old_string)
      : 0

  return { path: filePath, basename: path.basename(filePath), action, additions, deletions }
}

// isFailed reads the harness' failure signal out of tool_response.
//
// Observed shapes (WorkBuddy 5.5.6, Bash):
//
//	success: {exitCode: 0, signal: null, sandboxDenied: false, tool_error_code: "0"}
//	failure: {exitCode: 1, tool_error_code: "8002", is_error: true, error: "..."}
//
// tool_error_code is a *string*, so "0" is truthy — a naive truthiness check
// marks every single call as failed. Every branch below is explicit for that
// reason. A command that swallows its own exit status ("ls missing; echo $?")
// legitimately reports success, which is why exitCode alone is not enough.
function isFailed(payload) {
  const response = payload.tool_response
  if (response && typeof response === 'object') {
    if (response.is_error === true) return true
    if (response.sandboxDenied === true) return true
    if (response.success === false) return true
    if (response.tool_error_code !== undefined && response.tool_error_code !== null) {
      const code = String(response.tool_error_code).trim().toLowerCase()
      if (code !== '' && code !== '0' && code !== 'ok' && code !== 'none') return true
    }
    if (response.exitCode !== undefined && response.exitCode !== null && Number(response.exitCode) !== 0) {
      return true
    }
    return false
  }
  // Not every agent returns an object; a plain string is only a failure when it
  // actually starts with the word.
  if (typeof response === 'string') return /^(error|failed)\b/i.test(response.trim())
  return false
}

function firstLine(value, limit) {
  const text = String(value || '').replace(/\s+/g, ' ').trim()
  if (!text) return ''
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text
}

function promptTitle(value) {
  const raw = String(value || '')
  const request = raw.match(/## My request:\s*([\s\S]*)/i)?.[1] || raw
  return firstLine(request.replace(/<image[\s\S]*?<\/image>/gi, ''), 80)
}

// --- persistence ----------------------------------------------------------------

function appendStream(sessionId, steps) {
  const file = path.join(STREAM_DIR, `${sessionId}.jsonl`)
  fs.mkdirSync(STREAM_DIR, { recursive: true })
  fs.appendFileSync(file, `${steps.map(step => JSON.stringify(step)).join('\n')}\n`)
  trimStream(file)
}

// trimStream keeps the newest STREAM_LIMIT lines. Called after every append,
// which is cheap because the file is bounded by STREAM_LIMIT.
function trimStream(file) {
  let content
  try {
    content = fs.readFileSync(file, 'utf8')
  } catch {
    return
  }
  const lines = content.split('\n')
  if (lines[lines.length - 1] === '') lines.pop()
  if (lines.length <= STREAM_LIMIT) return
  const kept = lines.slice(-STREAM_LIMIT)
  try {
    fs.writeFileSync(file, `${kept.join('\n')}\n`)
  } catch {
    // Best effort.
  }
}

// updateLedger upserts the touched file into files.d and returns the roll-up
// the header shows ("7 个文件 +495 −5").
function updateLedger(sessionId, project, event, payload) {
  const file = path.join(FILES_DIR, `${sessionId}.json`)
  let ledger = { sessionId, project, files: [] }
  try {
    ledger = JSON.parse(fs.readFileSync(file, 'utf8'))
    if (!Array.isArray(ledger.files)) ledger.files = []
  } catch {
    // First write for this session.
  }
  ledger.sessionId = sessionId
  ledger.project = project || ledger.project || ''

  if (event === 'PostToolUse') {
    const change = fileChange(payload)
    if (change && !isFailed(payload)) {
      const existing = ledger.files.find(item => item.path === change.path)
      if (existing) {
        // The action is deliberately kept from the first touch: a file this
        // session created and then edited is still a file it created.
        existing.additions += change.additions
        existing.deletions += change.deletions
        existing.touches = (existing.touches || 1) + 1
        existing.ts = Date.now() / 1000
      } else {
        ledger.files.push({
          path: change.path,
          name: change.basename,
          action: change.action,
          additions: change.additions,
          deletions: change.deletions,
          touches: 1,
          ts: Date.now() / 1000,
        })
      }
    }
  }

  const additions = ledger.files.reduce((sum, item) => sum + (item.additions || 0), 0)
  const deletions = ledger.files.reduce((sum, item) => sum + (item.deletions || 0), 0)

  if (event === 'PostToolUse' || ledger.files.length === 0) {
    try {
      fs.mkdirSync(FILES_DIR, { recursive: true })
      fs.writeFileSync(file, JSON.stringify(ledger))
    } catch {
      // Best effort.
    }
  }

  return { fileCount: ledger.files.length, additions, deletions }
}

function readState(sessionId) {
  try {
    return JSON.parse(fs.readFileSync(path.join(STATE_DIR, `${sessionId}.json`), 'utf8')) || {}
  } catch {
    return {}
  }
}

function writeState(sessionId, record) {
  try {
    fs.mkdirSync(STATE_DIR, { recursive: true })
    fs.writeFileSync(path.join(STATE_DIR, `${sessionId}.json`), JSON.stringify(record))
  } catch {
    // Best effort.
  }
}

// --- debug ----------------------------------------------------------------------

function capture(payload) {
  try {
    if (!fs.existsSync(CAPTURE_FLAG)) return
    fs.appendFileSync(CAPTURE_FILE, `${JSON.stringify(payload)}\n`)
  } catch {
    // Debug only.
  }
}
