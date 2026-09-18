#!/usr/bin/env node
// Contract tests for scripts/even-workbuddy-status-writer.mjs.
//
//   node scripts/even-workbuddy-status-writer.test.mjs
//
// Runs the real writer as a subprocess with synthetic hook payloads against a
// temporary EVEN_STATUS_ROOT, then asserts on the three artifacts. The
// transcript tail is exercised against a synthetic JSONL so the test does not
// depend on any live session.
import assert from 'node:assert/strict'
import {spawnSync} from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'

const WRITER = path.join(import.meta.dirname, 'even-workbuddy-status-writer.mjs')
const SESSION = 'test-session-0001'

let root
let failures = 0
let checks = 0

function check(name, fn) {
  checks++
  try {
    fn()
    console.log(`  ok   ${name}`)
  } catch (error) {
    failures++
    console.log(`  FAIL ${name}`)
    console.log(`       ${error.message.split('\n').join('\n       ')}`)
  }
}

// hook runs the writer exactly the way the hook config does.
function hook(payload) {
  const result = spawnSync(process.execPath, [WRITER, String(payload.hook_event_name || '')], {
    input: JSON.stringify(payload),
    env: {...process.env, EVEN_STATUS_ROOT: root},
    encoding: 'utf8',
  })
  assert.equal(result.status, 0, `writer exited ${result.status}: ${result.stderr}`)
}

const state = () => JSON.parse(fs.readFileSync(path.join(root, 'state.d', `${SESSION}.json`), 'utf8'))
const steps = () =>
  fs
    .readFileSync(path.join(root, 'stream.d', `${SESSION}.jsonl`), 'utf8')
    .split('\n')
    .filter(Boolean)
    .map(line => JSON.parse(line))
const ledger = () => JSON.parse(fs.readFileSync(path.join(root, 'files.d', `${SESSION}.json`), 'utf8'))

const base = {session_id: SESSION, cwd: '/Users/x/even-app'}

function freshRoot() {
  root = fs.mkdtempSync(path.join(os.tmpdir(), 'even-writer-'))
}

console.log('even-workbuddy-status-writer contract')

// --- 1. lifecycle mapping -------------------------------------------------------
freshRoot()
check('UserPromptSubmit emits a prompt step and enters thinking', () => {
  hook({...base, hook_event_name: 'UserPromptSubmit', prompt: '把改动文件推给眼镜'})
  const [step] = steps()
  assert.equal(step.kind, 'prompt')
  assert.equal(step.text, '把改动文件推给眼镜')
  assert.equal(state().state, 'thinking')
})

check('a new prompt resets this-turn time, changes and recent activity', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: {file_path: '/Users/x/even-app/old.go', old_string: 'a', new_string: 'b'},
    tool_response: 'Successfully edited file',
  })
  assert.equal(state().changeCount, 1)
  const before = Date.now() / 1000
  hook({...base, hook_event_name: 'UserPromptSubmit', prompt: '开始下一轮'})
  const record = state()
  assert.equal(record.changeCount, 0)
  assert.ok(record.startedAt >= before)
  assert.deepEqual(steps().map(step => step.text), ['开始下一轮'])
  assert.equal(ledger().files.length, 0)
})

check('PreToolUse carries the live indicator and adds no stream row', () => {
  const before = steps().length
  hook({...base, hook_event_name: 'PreToolUse', tool_name: 'Bash', tool_input: {command: 'go test ./...'}})
  assert.equal(steps().length, before, 'PreToolUse must not settle a step')
  const record = state()
  assert.equal(record.state, 'tool')
  assert.equal(record.currentTool, 'Bash')
  assert.equal(record.current, 'go test ./...')
})

check('AskUserQuestion switches the state to needs_input', () => {
  hook({...base, hook_event_name: 'PreToolUse', tool_name: 'AskUserQuestion', tool_input: {}})
  assert.equal(state().state, 'needs_input')
})

check('PostToolUse settles a step and clears the live indicator', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: {file_path: '/Users/x/even-app/statusmonitor.go', content: 'a\nb\nc'},
    tool_response: 'Successfully created and wrote to new file: /Users/x/even-app/statusmonitor.go',
    call_id: 'call_1',
  })
  const last = steps().at(-1)
  assert.equal(last.kind, 'tool')
  assert.equal(last.status, 'ok')
  assert.equal(last.tool, 'Write')
  assert.equal(last.text, 'statusmonitor.go')
  assert.equal(last.action, 'create')
  assert.equal(last.add, 3)
  assert.equal(last.del, 0)
  assert.equal(state().current, '', 'a settled tool must not leave a stale live line')
})

// --- 2. file ledger -------------------------------------------------------------
check('the ledger records the created file once', () => {
  const files = ledger().files
  assert.equal(files.length, 1)
  assert.equal(files[0].name, 'statusmonitor.go')
  assert.equal(files[0].action, 'create')
  assert.equal(files[0].additions, 3)
  assert.equal(state().fileCount, 1)
})

check('repeated edits accumulate into the same ledger row', () => {
  for (const [i, pair] of [['old', 'new\nnew'], ['x', 'y']].entries()) {
    hook({
      ...base,
      hook_event_name: 'PostToolUse',
      tool_name: 'Edit',
      tool_input: {file_path: '/Users/x/even-app/statusmonitor.go', old_string: pair[0], new_string: pair[1]},
      tool_response: 'Successfully edited file',
      call_id: `call_edit_${i}`,
    })
  }
  const files = ledger().files
  assert.equal(files.length, 1, 'one file must stay one row')
  assert.equal(files[0].touches, 3)
  assert.equal(files[0].additions, 3 + 2 + 1)
  assert.equal(files[0].deletions, 0 + 1 + 1)
  assert.equal(state().additions, files[0].additions)
})

check('a write over an existing file is not recorded as a create', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: {file_path: '/Users/x/even-app/evenservice.go', content: 'x\ny'},
    tool_response: 'Successfully wrote to /Users/x/even-app/evenservice.go',
    call_id: 'call_write_overwrite',
  })
  const row = ledger().files.find(file => file.path.endsWith('evenservice.go'))
  assert.equal(row.action, 'modify')
})

check('a file created earlier in the session keeps its create action', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: {file_path: '/Users/x/even-app/statusmonitor.go', content: 'z'},
    tool_response: 'Successfully wrote to /Users/x/even-app/statusmonitor.go',
    call_id: 'call_write_again',
  })
  const row = ledger().files.find(file => file.path.endsWith('statusmonitor.go'))
  assert.equal(row.action, 'create', 'the session did create this file')
  assert.equal(row.touches, 4)
})

check('a failed tool call never touches the ledger', () => {
  const before = ledger().files.length
  hook({
    ...base,
    hook_event_name: 'PostToolUseFailure',
    tool_name: 'Write',
    tool_input: {file_path: '/Users/x/even-app/broken.go', content: 'x'},
    call_id: 'call_fail',
  })
  assert.equal(ledger().files.length, before)
  assert.equal(steps().at(-1).status, 'fail')
})

check('a non-zero exit code marks the bash step as failed', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: 'go build ./...'},
    tool_response: {exitCode: 1, signal: null, sandboxDenied: false, tool_error_code: '8002', is_error: true, error: 'Exit Code: 1'},
    call_id: 'call_bash_fail',
  })
  assert.equal(steps().at(-1).status, 'fail')
  assert.equal(steps().at(-1).tool, 'Bash')
})

// WorkBuddy reports tool_error_code as the *string* "0" on success, which is
// truthy — the exact shape that silently marked every call as failed.
check('the real success shape is not mistaken for a failure', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: 'go test ./...'},
    tool_response: {exitCode: 0, signal: null, interrupted: false, sandboxDenied: false, stdoutBytesTruncated: 0, stderrBytesTruncated: 0, tool_error_code: '0'},
    call_id: 'call_bash_ok',
  })
  assert.equal(steps().at(-1).status, 'ok')
})

check('a swallowed exit status still reports success', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: 'ls /missing 2>&1; echo "inner exit=$?"'},
    tool_response: {exitCode: 0, signal: null, sandboxDenied: false, tool_error_code: '0'},
    call_id: 'call_bash_swallowed',
  })
  assert.equal(steps().at(-1).status, 'ok')
})

check('sandbox denial is a failure even with a zero exit code', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: 'rm -rf /tmp/x'},
    tool_response: {exitCode: 0, sandboxDenied: true, tool_error_code: '0'},
    call_id: 'call_bash_denied',
  })
  assert.equal(steps().at(-1).status, 'fail')
})

check('a successful write is never read as a failure', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: {file_path: '/Users/x/even-app/ok.go', content: 'x'},
    tool_response: 'Successfully created and wrote to new file: /Users/x/even-app/ok.go',
    call_id: 'call_write_ok',
  })
  assert.equal(steps().at(-1).status, 'ok')
})

check('a leading cd is stripped so the real command survives clipping', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: 'cd /Users/x/even-app && go test ./... 2>&1 | tail -5', description: 'run tests'},
    call_id: 'call_bash_cd',
  })
  const step = steps().at(-1)
  assert.equal(step.text, 'go test ./... 2>&1 | tail -5')
  assert.equal(step.status, 'ok')
})

check('an absolute interpreter path does not eat the row', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: `${os.homedir()}/.workbuddy/binaries/node/versions/22/bin/node scripts/probe.mjs --flag`},
    call_id: 'call_bash_interp',
  })
  assert.equal(steps().at(-1).text, 'scripts/probe.mjs --flag')
})

check('a bare interpreter is dropped too, and home becomes ~', () => {
  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: `cat ${os.homedir()}/notes.md`},
    call_id: 'call_bash_home',
  })
  assert.equal(steps().at(-1).text, 'cat ~/notes.md')

  hook({
    ...base,
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_input: {command: 'python3 -m pytest tests/'},
    call_id: 'call_bash_py',
  })
  assert.equal(steps().at(-1).text, '-m pytest tests/')
})

// --- 3. ask / notify ------------------------------------------------------------
check('permission prompts become an ask step and the permission state', () => {
  hook({...base, hook_event_name: 'Notification', notification_type: 'permission_prompt', message: 'needs your permission to use Bash'})
  assert.equal(state().state, 'permission')
  assert.equal(steps().at(-1).kind, 'ask')
  assert.equal(steps().at(-1).text, '需要你的授权')
})

check('a permission request names the thing being asked about', () => {
  hook({...base, hook_event_name: 'PermissionRequest', tool_name: 'Bash', tool_input: {command: 'rm -rf build'}})
  assert.equal(state().state, 'permission')
  assert.equal(steps().at(-1).kind, 'ask')
  // The adapter records the fact (which tool, what target); the view turns it
  // into "运行 rm -rf build" behind its "?" gutter.
  assert.equal(steps().at(-1).tool, 'Bash')
  assert.equal(steps().at(-1).text, 'rm -rf build')
})

check('idle notifications read as a waiting state, not a permission', () => {
  hook({...base, hook_event_name: 'Notification', notification_type: 'idle_prompt', message: 'CodeBuddy is waiting for your input'})
  assert.equal(state().state, 'needs_input')
  assert.equal(steps().at(-1).text, 'CodeBuddy is waiting for your input')
})

check('interrupt notifications and stop failures pause the task', () => {
  hook({...base, hook_event_name: 'Notification', notification_type: 'turn_interrupted', message: 'Task interrupted'})
  assert.equal(state().state, 'paused')

  hook({...base, hook_event_name: 'StopFailure'})
  assert.equal(state().state, 'paused')
})

check('compaction and stop settle their own steps', () => {
  hook({...base, hook_event_name: 'PreCompact', trigger: 'auto'})
  assert.equal(state().state, 'compacting')
  assert.equal(steps().at(-1).kind, 'note')

  hook({...base, hook_event_name: 'Stop'})
  assert.equal(state().state, 'done')
  assert.equal(steps().at(-1).kind, 'done')
})

check('unmapped events are ignored entirely', () => {
  const before = steps().length
  hook({...base, hook_event_name: 'ConfigChange'})
  assert.equal(steps().length, before)
})

check('payloads without a session id are ignored', () => {
  const before = steps().length
  hook({hook_event_name: 'PostToolUse', tool_name: 'Bash', tool_input: {command: 'ls'}})
  assert.equal(steps().length, before)
})

// --- 4. transcript tail ---------------------------------------------------------
function writeTranscript(file, records) {
  fs.writeFileSync(file, records.map(record => `${JSON.stringify(record)}\n`).join(''))
}

freshRoot()
const transcript = path.join(root, 'session.jsonl')
check('the transcript supplies narration and the conversation title', () => {
  writeTranscript(transcript, [
    {type: 'ai-title', aiTitle: 'Codex消息推送与workbuddy状态集成', timestamp: 1},
    {type: 'reasoning', content: []},
    {type: 'message', role: 'user', content: [{type: 'input_text', text: 'hi'}]},
    {type: 'message', role: 'assistant', content: [{type: 'output_text', text: '先看 SDK 能力。\n第二行不该出现'}]},
  ])
  hook({...base, hook_event_name: 'SessionStart', transcript_path: transcript})
  const said = steps().filter(step => step.kind === 'say')
  assert.equal(said.length, 1)
  assert.equal(said[0].text, '先看 SDK 能力。 第二行不该出现')
  assert.equal(state().title, 'Codex消息推送与workbuddy状态集成')
  assert.equal(fs.readFileSync(path.join(root, 'state.d', `${SESSION}.title`), 'utf8'), 'Codex消息推送与workbuddy状态集成')
})

check('only newly appended bytes are consumed (no re-reading)', () => {
  const before = steps().filter(step => step.kind === 'say').length
  hook({...base, hook_event_name: 'Stop', transcript_path: transcript})
  assert.equal(steps().filter(step => step.kind === 'say').length, before, 'unchanged transcript must add nothing')

  fs.appendFileSync(transcript, `${JSON.stringify({type: 'message', role: 'assistant', content: [{type: 'output_text', text: '第二步完成。'}]})}\n`)
  hook({...base, hook_event_name: 'Stop', transcript_path: transcript})
  const said = steps().filter(step => step.kind === 'say')
  assert.equal(said.length, before + 1)
  assert.equal(said.at(-1).text, '第二步完成。')
})

check('a half-written trailing line is left for the next flush', () => {
  const before = steps().filter(step => step.kind === 'say').length
  fs.appendFileSync(transcript, '{"type":"message","role":"assistant","content":[{"type":"outpu')
  hook({...base, hook_event_name: 'Stop', transcript_path: transcript})
  assert.equal(steps().filter(step => step.kind === 'say').length, before)

  fs.appendFileSync(transcript, 't_text","text":"补全"}]}\n')
  hook({...base, hook_event_name: 'Stop', transcript_path: transcript})
  const said = steps().filter(step => step.kind === 'say')
  assert.equal(said.length, before + 1)
  assert.equal(said.at(-1).text, '补全')
})

// --- 5. caps --------------------------------------------------------------------
check('the stream is capped and keeps the newest entries', () => {
  const file = path.join(root, 'stream.d', `${SESSION}.jsonl`)
  const filler = Array.from({length: 700}, (_, i) => JSON.stringify({ts: i, kind: 'tool', tool: 'Bash', text: `cmd-${i}`}))
  fs.appendFileSync(file, `${filler.join('\n')}\n`)
  hook({...base, hook_event_name: 'PostToolUse', tool_name: 'Bash', tool_input: {command: 'tail-marker'}, call_id: 'c'})
  const all = steps()
  assert.ok(all.length <= 600, `stream grew to ${all.length} lines`)
  assert.equal(all.at(-1).text, 'tail-marker')
  assert.notEqual(all[0].text, 'cmd-0', 'oldest entries must be dropped first')
})

console.log(`\n${checks - failures}/${checks} checks passed`)
if (failures > 0) process.exit(1)
