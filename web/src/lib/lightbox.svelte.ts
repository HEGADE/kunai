// The one full-size image viewer.
//
// A store rather than local state in Markdown.svelte, because Markdown is
// rendered once per assistant block and there are dozens on screen in a long
// conversation. Keeping the overlay there would mount dozens of them, each with
// its own key handler and its own idea of whether it is open; the viewer is a
// property of the window, so it lives in one place and every image asks the same
// object to show it.

interface Shown {
  src: string
  alt: string
}

class Lightbox {
  current = $state<Shown | null>(null)

  get open(): boolean {
    return this.current !== null
  }

  show(src: string, alt = '') {
    if (!src) return
    this.current = { src, alt }
  }

  close() {
    this.current = null
  }
}

export const lightbox = new Lightbox()
