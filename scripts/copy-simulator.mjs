import { createRequire } from 'node:module'
import { chmodSync, copyFileSync, mkdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'

const require = createRequire(resolve(process.cwd(), 'frontend/package.json'))
const [destination, architecture = process.arch] = process.argv.slice(2)
const arch = architecture === 'amd64' ? 'x64' : architecture
const binary = require.resolve(`@evenrealities/sim-darwin-${arch}/bin/evenhub-simulator`)
const output = resolve(destination, 'evenhub-simulator')
mkdirSync(dirname(output), { recursive: true })
copyFileSync(binary, output)
chmodSync(output, 0o755)
