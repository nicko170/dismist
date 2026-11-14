import { execFileSync, spawn, type ChildProcess } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import path from 'node:path'

// Live end-to-end runs (design §7.10): dismistd from this checkout plus
// tools/fakeap with synthetic frames. Enabled with E2E_LIVE=1. It builds the
// real UI (embedded by go build), starts dismistd on free loopback ports
// with a throwaway database and a self-signed WSS certificate, signs in
// with the generated admin token, creates a site and registers one fake AP,
// then starts fakeap against it. Nothing here touches a real AP or network
// beyond 127.0.0.1. E2E_LIVE_SKIP_BUILD=1 reuses existing binaries.

const ui = path.resolve(import.meta.dirname, '../..')
const repo = path.resolve(ui, '..')
export const liveDir = path.join(ui, 'test-results', 'live')
export const liveState = path.join(liveDir, 'state.json')
export const liveStorage = path.join(liveDir, 'storage.json')

export type LiveState = { baseURL: string; adminToken: string; siteId: string; apId: string; pids: number[] }

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = createServer()
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      server.close(() => (typeof address === 'object' && address ? resolve(address.port) : reject(new Error('no port'))))
    })
  })
}

async function waitFor(check: () => Promise<boolean>, what: string, ms = 60_000) {
  const until = Date.now() + ms
  while (Date.now() < until) {
    try {
      if (await check()) return
    } catch {
      /* not yet */
    }
    await new Promise((resolve) => setTimeout(resolve, 250))
  }
  throw new Error(`timed out waiting for ${what}`)
}

function start(command: string, args: string[], log: string): ChildProcess {
  const child = spawn(command, args, { cwd: liveDir, stdio: ['ignore', 'pipe', 'pipe'], detached: false })
  const chunks: Buffer[] = []
  child.stdout?.on('data', (chunk: Buffer) => chunks.push(chunk))
  child.stderr?.on('data', (chunk: Buffer) => chunks.push(chunk))
  child.on('exit', () => writeFileSync(path.join(liveDir, log), Buffer.concat(chunks)))
  return child
}

export default async function globalSetup() {
  mkdirSync(liveDir, { recursive: true })
  const bin = path.join(liveDir, 'bin')
  if (!process.env.E2E_LIVE_SKIP_BUILD) {
    execFileSync('npm', ['run', 'build'], { cwd: ui, stdio: 'inherit' })
    execFileSync('go', ['build', '-o', path.join(bin, 'dismistd'), './cmd/dismistd'], { cwd: repo, stdio: 'inherit' })
    execFileSync('go', ['build', '-o', path.join(bin, 'fakeap'), './tools/fakeap/cmd/fakeap'], { cwd: repo, stdio: 'inherit' })
  }
  execFileSync('openssl', [
    'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:prime256v1', '-nodes', '-days', '1',
    '-keyout', path.join(liveDir, 'tls.key'), '-out', path.join(liveDir, 'tls.crt'),
    '-subj', '/CN=127.0.0.1', '-addext', 'subjectAltName=IP:127.0.0.1',
  ], { stdio: 'ignore' })

  const [uiPort, wsPort, dnsPort] = [await freePort(), await freePort(), await freePort()]
  const baseURL = `http://127.0.0.1:${uiPort}`
  const dismistd = start(path.join(bin, 'dismistd'), [
    '-db', path.join(liveDir, `controller-${process.pid}.db`),
    '-ui-listen', `127.0.0.1:${uiPort}`,
    '-ws-listen', `127.0.0.1:${wsPort}`,
    '-dns-listen', `127.0.0.1:${dnsPort}`,
    '-controller-ip', '127.0.0.1',
    '-upstream-dns', '127.0.0.1:9',
    '-tls-cert', path.join(liveDir, 'tls.crt'),
    '-tls-key', path.join(liveDir, 'tls.key'),
    '-tls-check-interval', '0',
    '-admin-token-file', path.join(liveDir, 'admin.token'),
  ], 'dismistd.log')
  await waitFor(async () => (await fetch(`${baseURL}/healthz`)).ok, 'dismistd /healthz')
  const adminToken = readFileSync(path.join(liveDir, 'admin.token'), 'utf8').trim()

  // Sign in once for the specs that start signed in.
  const login = await fetch(`${baseURL}/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: baseURL }, body: JSON.stringify({ token: adminToken }) })
  if (!login.ok) throw new Error(`login: ${login.status} ${await login.text()}`)
  const cookie = login.headers.getSetCookie()[0]
  const [pair] = cookie.split(';')
  const [name, value] = pair.split('=')
  writeFileSync(liveStorage, JSON.stringify({ cookies: [{ name, value, domain: '127.0.0.1', path: '/', expires: -1, httpOnly: true, secure: false, sameSite: 'Strict' }], origins: [] }))
  const headers = { 'Content-Type': 'application/json', Origin: baseURL, Cookie: pair }

  const siteResponse = await fetch(`${baseURL}/api/sites`, { method: 'POST', headers, body: JSON.stringify({ name: 'Home', country: 'AU', timezone: 'Australia/Sydney' }) })
  if (!siteResponse.ok) throw new Error(`create site: ${siteResponse.status} ${await siteResponse.text()}`)
  const site = (await siteResponse.json()) as { id?: string; site?: { id: string } }
  const siteId = site.id ?? site.site?.id ?? ''
  const apId = '02-aa-00-00-00-01'
  const secret = randomBytes(32).toString('hex')
  const apResponse = await fetch(`${baseURL}/api/aps`, { method: 'POST', headers, body: JSON.stringify({ id: apId, name: 'Hallway', site_id: siteId, secret }) })
  if (!apResponse.ok) throw new Error(`register AP: ${apResponse.status} ${await apResponse.text()}`)

  const fakeap = start(path.join(bin, 'fakeap'), ['-url', `wss://127.0.0.1:${wsPort}/ws`, '-id', apId, '-secret', secret, '-insecure', '-fast', '2s', '-full', '10s', '-clients', '3'], 'fakeap.log')
  await waitFor(async () => {
    const list = await fetch(`${baseURL}/api/aps?site=${encodeURIComponent(siteId)}`, { headers })
    const body = (await list.json()) as unknown
    const rows = (Array.isArray(body) ? body : ((body as { aps?: unknown[] }).aps ?? [])) as { id: string; connected?: boolean }[]
    return rows.some((row) => row.id === apId && row.connected)
  }, 'fakeap to connect')

  const state: LiveState = { baseURL, adminToken, siteId, apId, pids: [dismistd.pid!, fakeap.pid!] }
  writeFileSync(liveState, JSON.stringify(state))
  process.env.E2E_BASE_URL = baseURL
  return async () => {
    fakeap.kill('SIGTERM')
    dismistd.kill('SIGTERM')
    // The real build replaced the committed stub; put it back (design §7.1).
    if (!process.env.E2E_LIVE_SKIP_BUILD) {
      try {
        execFileSync('git', ['checkout', '--', 'dist/index.html'], { cwd: ui, stdio: 'ignore' })
      } catch {
        /* not a git checkout (CI artefact): nothing to restore */
      }
    }
  }
}
