#!/usr/bin/env node
// Registers the Even Control status hooks in the WorkBuddy user settings.
//
// Usage:
//   node scripts/install-workbuddy-status-hooks.mjs
//   node scripts/install-workbuddy-status-hooks.mjs --target ~/.codebuddy/settings.json
//   node scripts/install-workbuddy-status-hooks.mjs --out /tmp/settings.json
//
// Idempotent: existing Even Control hook groups are replaced, everything else
// (including other tools' hooks) is preserved. A .bak copy is written first.
//
// --out writes the merged result elsewhere instead of in place. WorkBuddy
// protects ~/.workbuddy/settings.json against truncating writes from inside a
// sandboxed session, so this is the escape hatch: merge to a temp file, then
// copy it over from a normal terminal.
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import process from 'node:process'

const EVENTS = [
  'SessionStart',
  'UserPromptSubmit',
  'PreToolUse',
  'PostToolUse',
  'PostToolUseFailure',
  'Notification',
  'PermissionRequest',
  'Elicitation',
  'PreCompact',
  'PostCompact',
  'SubagentStart',
  'SubagentStop',
  'TaskCreated',
  'TaskCompleted',
  'TeammateIdle',
  'Stop',
  'SessionEnd',
]

const MARKER = 'even-workbuddy-status-writer.mjs'
const DEFAULT_SETTINGS = path.join(os.homedir(), '.workbuddy', 'settings.json')

const targetIndex = process.argv.indexOf('--target')
const targetArg = targetIndex > -1 ? process.argv[targetIndex + 1] : undefined
const settingsFile = targetArg ? path.resolve(targetArg) : DEFAULT_SETTINGS

const outIndex = process.argv.indexOf('--out')
const outArg = outIndex > -1 ? process.argv[outIndex + 1] : undefined
const outFile = outArg ? path.resolve(outArg) : settingsFile

const writer = path.resolve('scripts/even-workbuddy-status-writer.mjs')

if (!fs.existsSync(writer)) {
  console.error(`writer not found: ${writer}`)
  process.exit(1)
}

let settings = {}
if (fs.existsSync(settingsFile)) {
  const raw = fs.readFileSync(settingsFile, 'utf8')
  try {
    fs.writeFileSync(`${settingsFile}.bak`, raw)
  } catch {
    // Some protected locations reject the backup write; it is only a safety net.
  }
  try {
    settings = JSON.parse(raw || '{}')
  } catch (error) {
    console.error(`cannot parse ${settingsFile}: ${error.message}`)
    process.exit(1)
  }
}

settings.hooks ||= {}
for (const event of EVENTS) {
  const groups = Array.isArray(settings.hooks[event]) ? settings.hooks[event] : []
  const command = `"${process.execPath}" "${writer}" ${event}`
  settings.hooks[event] = [
    ...groups.filter(group => !(group.hooks || []).some(hook => String(hook.command).includes(MARKER))),
    { hooks: [{ type: 'command', command, timeout: 3, statusMessage: `Even Control: ${event}` }] },
  ]
}

fs.mkdirSync(path.dirname(outFile), { recursive: true })
fs.writeFileSync(outFile, `${JSON.stringify(settings, null, 2)}\n`)
console.log(`Installed Even Control WorkBuddy status hooks in ${settingsFile}`)
if (outFile !== settingsFile) {
  console.log(`Merged result written to ${outFile}`)
  console.log(`Apply it with:  cp "${outFile}" "${settingsFile}"`)
}
