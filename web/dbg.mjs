import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1280, height: 900 } })
p.on('pageerror', (e) => console.log('PAGEERROR', e.message))
await p.addInitScript(() => {
  const O = window.WebSocket
  window.__s = []
  window.WebSocket = class extends O { constructor(...a) { super(...a); window.__s.push(this) } }
})
await p.goto('http://127.0.0.1:8901/')
await p.waitForTimeout(1500)
await p.locator(".row:has(.node:not([data-state='past']))").first().click()
await p.locator('textarea[placeholder="Message Claude…"]').first().waitFor({ timeout: 10000 })
const send = (f) => p.evaluate((x) => {
  const ws = window.__s.filter((s) => s.url.includes('/ws/app/')).pop()
  ws.dispatchEvent(new MessageEvent('message', { data: JSON.stringify(x) }))
}, f)

await send({ t: 'user', seq: 501, text: 'go' })
await send({ t: 'state', seq: 502, state: 'running' })
await send({ t: 'assistant', seq: 503, blocks: [
  { type: 'tool_use', id: 'd1', name: 'Read', input: { file_path: '/tmp/alpha/.kunai-review/pr-4.diff' } },
  { type: 'tool_use', id: 'g1', name: 'Read', input: { file_path: '/tmp/alpha/a.go' } },
]})
await send({ t: 'tool_result', seq: 504, tool_use_id: 'd1', content: '@@ -1,3 +1,4 @@\n ctx\n-old line\n+new line\n+added\n' })
await send({ t: 'tool_result', seq: 505, tool_use_id: 'g1', content: '     1→package a\n     2→\n     3→func A() int { return 1 }\n' })
await p.waitForTimeout(500)

await p.locator('.la .head').click()          // open the activity stream
await p.waitForTimeout(400)
const cards = p.locator('.la button').filter({ hasText: 'pr-4.diff' })
await cards.first().click()                    // expand the diff read
await p.waitForTimeout(500)
console.log('diff spans:', await p.locator('.hljs-addition, .hljs-deletion').count())
const goCard = p.locator('.la button').filter({ hasText: 'a.go' })
await goCard.first().click()
await p.waitForTimeout(500)
console.log('go keyword spans:', await p.locator('.hljs-keyword').count())
console.log('arrows still present:', (await p.locator('.la').innerText()).includes('→'))
await p.screenshot({ path: '/tmp/hl.png', fullPage: true })
await b.close()
