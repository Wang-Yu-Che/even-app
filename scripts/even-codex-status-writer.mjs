#!/usr/bin/env node
// Codex uses the same tested status-stream contract as WorkBuddy. This thin
// Adapter only selects the source directory and tool-name compatibility layer.
process.env.EVEN_AGENT = 'codex'
await import('./even-workbuddy-status-writer.mjs')
