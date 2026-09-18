#!/usr/bin/env node
import assert from 'node:assert/strict'
import {spawnSync} from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'

const writer = path.join(import.meta.dirname, 'even-codex-status-writer.mjs')
const root = fs.mkdtempSync(path.join(os.tmpdir(), 'even-codex-writer-'))
const sessionId = 'codex-session-1'
const base = {sessionId, workdir: '/Users/x/even-app'}

function hook(event, payload = {}) {
  const result = spawnSync(process.execPath, [writer, event], {
    input: JSON.stringify({...base, ...payload}),
    env: {...process.env, EVEN_STATUS_ROOT: root},
    encoding: 'utf8',
  })
  assert.equal(result.status, 0, result.stderr)
}

const state = () => JSON.parse(fs.readFileSync(path.join(root, 'state.d', `${sessionId}.json`), 'utf8'))
const steps = () => fs.readFileSync(path.join(root, 'stream.d', `${sessionId}.jsonl`), 'utf8').trim().split('\n').map(JSON.parse)
const ledger = () => JSON.parse(fs.readFileSync(path.join(root, 'files.d', `${sessionId}.json`), 'utf8'))

hook('UserPromptSubmit', {userPrompt: '把通知推到眼镜'})
assert.equal(steps().at(-1).text, '把通知推到眼镜')
assert.equal(state().source, 'codex')

hook('PreToolUse', {toolName: 'functions.exec_command', input: {cmd: 'go test ./...'}})
assert.equal(state().currentTool, 'Bash')
assert.equal(state().current, 'go test ./...')

hook('PostToolUse', {toolName: 'functions.exec_command', input: {cmd: 'go test ./...'}, output: {exitCode: 1, is_error: true}})
assert.equal(steps().at(-1).status, 'fail')

hook('PostToolUse', {
  tool_name: 'apply_patch',
  tool_input: {command: '*** Begin Patch\n*** Update File: /Users/x/even-app/App.tsx\n@@\n-old\n+new\n+more\n*** End Patch'},
  output: {success: true},
})
assert.equal(steps().at(-1).tool, 'Edit')
assert.equal(steps().at(-1).text, 'App.tsx')
assert.equal(ledger().files[0].name, 'App.tsx')
assert.equal(ledger().files[0].additions, 2)
assert.equal(ledger().files[0].deletions, 1)

hook('PermissionRequest', {toolName: 'functions.exec_command', input: {cmd: 'make install'}})
assert.equal(state().state, 'permission')
assert.equal(steps().at(-1).text, 'make install')

hook('PreToolUse', {toolName: 'webrun', input: {search_query: [{q: 'G2 glyph support'}]}})
assert.equal(state().currentTool, 'WebSearch')
assert.equal(state().current, 'G2 glyph support')

hook('PostToolUse', {toolName: 'webrun', input: {search_query: [{q: 'G2 glyph support'}]}, output: {success: true}})
assert.equal(steps().at(-1).tool, 'WebSearch')
assert.equal(steps().at(-1).text, 'G2 glyph support')

console.log('codex status writer contract: ok')
