// Syntax highlighting via highlight.js core with an explicit, curated language
// set — synchronous and tree-shakeable, so it fits the embedded PWA and the
// sync markdown render path. We never auto-detect (slow, pulls every grammar).

import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import json from 'highlight.js/lib/languages/json'
import python from 'highlight.js/lib/languages/python'
import go from 'highlight.js/lib/languages/go'
import rust from 'highlight.js/lib/languages/rust'
import yaml from 'highlight.js/lib/languages/yaml'
import markdown from 'highlight.js/lib/languages/markdown'
import css from 'highlight.js/lib/languages/css'
import xml from 'highlight.js/lib/languages/xml'
import sql from 'highlight.js/lib/languages/sql'
import diff from 'highlight.js/lib/languages/diff'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import plaintext from 'highlight.js/lib/languages/plaintext'

let registered = false
export function registerLanguages(): void {
  if (registered) return
  registered = true
  const langs: Record<string, unknown> = {
    bash,
    javascript,
    typescript,
    json,
    python,
    go,
    rust,
    yaml,
    markdown,
    css,
    xml,
    sql,
    diff,
    dockerfile,
    plaintext,
  }
  for (const [name, def] of Object.entries(langs)) {
    hljs.registerLanguage(name, def as never)
  }
  // Common aliases.
  hljs.registerAliases(['sh', 'shell', 'zsh'], { languageName: 'bash' })
  hljs.registerAliases(['js', 'jsx'], { languageName: 'javascript' })
  hljs.registerAliases(['ts', 'tsx'], { languageName: 'typescript' })
  hljs.registerAliases(['py'], { languageName: 'python' })
  hljs.registerAliases(['rs'], { languageName: 'rust' })
  hljs.registerAliases(['yml'], { languageName: 'yaml' })
  hljs.registerAliases(['md'], { languageName: 'markdown' })
  hljs.registerAliases(['html', 'svelte', 'vue'], { languageName: 'xml' })
  hljs.registerAliases(['docker'], { languageName: 'dockerfile' })
  hljs.registerAliases(['text', 'txt'], { languageName: 'plaintext' })
}

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

// highlightToHtml returns hljs token markup for a known language, else the
// plain (escaped) code. Safe to drop into innerHTML (goes through DOMPurify
// where used in Markdown; CodeView emits it directly).
export function highlightToHtml(code: string, lang?: string): string {
  registerLanguages()
  const name = lang ? resolveLang(lang) : undefined
  if (name && hljs.getLanguage(name)) {
    try {
      return hljs.highlight(code, { language: name, ignoreIllegals: true }).value
    } catch {
      /* fall through to plain */
    }
  }
  return escapeHtml(code)
}

function resolveLang(lang: string): string {
  return lang.trim().toLowerCase()
}

const EXT_LANG: Record<string, string> = {
  ts: 'typescript',
  tsx: 'typescript',
  js: 'javascript',
  jsx: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  json: 'json',
  py: 'python',
  go: 'go',
  rs: 'rust',
  yml: 'yaml',
  yaml: 'yaml',
  md: 'markdown',
  markdown: 'markdown',
  css: 'css',
  html: 'xml',
  htm: 'xml',
  xml: 'xml',
  svelte: 'xml',
  vue: 'xml',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  sql: 'sql',
  dockerfile: 'dockerfile',
  toml: 'plaintext',
}

// langFromPath maps a file path/name to a highlight language.
export function langFromPath(path: string): string | undefined {
  const base = path.replace(/\/+$/, '').split('/').pop() ?? ''
  if (/^dockerfile$/i.test(base)) return 'dockerfile'
  const ext = base.includes('.') ? base.split('.').pop()!.toLowerCase() : ''
  return EXT_LANG[ext]
}

// langLabel is the short uppercase tag shown on a code block's bar.
export function langLabel(lang?: string): string {
  if (!lang) return ''
  const map: Record<string, string> = {
    javascript: 'JS',
    typescript: 'TS',
    python: 'PY',
    markdown: 'MD',
    xml: 'HTML',
    plaintext: 'TXT',
  }
  const l = resolveLang(lang)
  return map[l] ?? l.toUpperCase()
}
