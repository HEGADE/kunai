// Saving a picture out of the conversation, and naming it something you would
// recognise in a downloads folder.
//
// Pure and free of Svelte so both the inline frame and the full-size viewer use
// one implementation, and so the naming rules can be exercised directly.

// FALLBACK_NAME is used when nothing better can be worked out. It carries an
// extension because a downloaded file with none is one the OS will not open.
const FALLBACK_NAME = 'image.png'

// fileNameFor works out what to call a saved picture.
//
// The src is kunai's own file route, which carries the real path in a query
// parameter (`?path=%2F…%2Fpic.png`), so the file's own name is right there and
// is far better than anything derived from the URL itself -- every image in a
// session shares the same route, so a name taken from the path segments would
// call all of them "file".
export function fileNameFor(src: string, alt = ''): string {
  const fromPath = pathParam(src)
  if (fromPath) {
    const base = fromPath.split('/').pop() ?? ''
    if (base) return base
  }
  // A data: URL has no name at all, so the alt text is the only description of
  // it that exists. Kept short and stripped of anything a filesystem dislikes.
  const fromAlt = slug(alt)
  if (fromAlt) return fromAlt + '.' + (extFromDataURL(src) || 'png')
  // Anything else: the last path segment of a plain URL, if it looks like a file.
  try {
    const base = new URL(src, location.href).pathname.split('/').pop() ?? ''
    if (base.includes('.')) return base
  } catch {
    // Not a parseable URL; the fallback below is the answer.
  }
  return FALLBACK_NAME
}

// pathParam pulls the `path` query parameter out of a file-route URL, which is
// where the real filename lives.
function pathParam(src: string): string {
  const q = src.indexOf('?')
  if (q < 0) return ''
  try {
    return new URLSearchParams(src.slice(q + 1)).get('path') ?? ''
  } catch {
    return ''
  }
}

function extFromDataURL(src: string): string {
  const m = /^data:image\/([a-z0-9.+-]+)/i.exec(src)
  if (!m) return ''
  const ext = m[1].toLowerCase()
  return ext === 'jpeg' ? 'jpg' : ext
}

// slug turns a caption into something safe to be a filename: a model-written alt
// text routinely contains slashes, quotes and commas.
function slug(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60)
}

// saveImage downloads a picture to the machine looking at it.
//
// It fetches the bytes and saves a blob rather than pointing an `<a download>`
// straight at the URL, and that is not belt-and-braces: the download attribute
// is IGNORED for a cross-origin URL, and cross-origin is the ordinary case here
// rather than an edge one, because a session on a peer machine is served from
// that machine's origin while the app came from the hub. Left as a plain link,
// clicking Download on a peer's image would navigate away from the conversation
// to show the picture, which is not what the button says.
//
// Returns false when it had to fall back, so a caller can say so.
export async function saveImage(url: string, name: string): Promise<boolean> {
  try {
    const res = await fetch(url)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const blob = await res.blob()
    const objectURL = URL.createObjectURL(blob)
    clickDownload(objectURL, name)
    // Revoked on a timer rather than immediately: some browsers have not started
    // reading the blob by the time the click handler returns.
    setTimeout(() => URL.revokeObjectURL(objectURL), 30_000)
    return true
  } catch {
    // CORS refused, offline, or the route said no. Opening it still puts the
    // picture somewhere the browser's own save can reach, which beats a button
    // that appears to do nothing.
    clickDownload(url, name)
    return false
  }
}

function clickDownload(href: string, name: string) {
  const a = document.createElement('a')
  a.href = href
  a.download = name
  a.rel = 'noopener'
  a.style.display = 'none'
  document.body.appendChild(a)
  a.click()
  a.remove()
}
