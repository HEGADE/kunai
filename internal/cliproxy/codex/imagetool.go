package codex

import (
	"encoding/base64"
	"log"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Making pictures in a Codex session.
//
// The premise had to be measured, because the obvious conclusion is wrong.
// chatgpt.com/backend-api/codex/responses reads like a coding endpoint, and
// Claude cannot draw at all, so the expected answer was "image generation needs
// an OpenAI platform key billed per picture, and no subscription covers it".
// It does not: that endpoint accepts the Responses API's built-in
// `{"type":"image_generation"}` tool on the same OAuth token a Codex provider
// already uses, and it both generates from a prompt and EDITS an image handed to
// it as an input_image. Proven live against a real ChatGPT login before any of
// this was written -- a 1254x1254 PNG each way, billed as ordinary tokens.
//
// Two facts decide the shape of this file.
//
// The `claude` CLI never asks for the tool, because it does not know it exists.
// So the proxy adds it on the way out rather than passing one through. That is
// the ONLY way this can work: kunai drives Claude Code, and no amount of
// prompting makes the CLI declare a tool from another vendor's API.
//
// And the picture comes back inside a stream the CLI is reading as an Anthropic
// response, which has no way to carry an image in an assistant message. So the
// image is written to disk and announced as markdown, which costs nothing to
// build: the client already turns `![alt](path)` in a reply into a request to
// the owner-only file route (withLocalImages in Markdown.svelte). Inlining it as
// a base64 data URL would also have rendered, and was rejected -- roughly a
// megabyte of base64 would go into the transcript and be replayed into the
// context of every later turn, which is a quarter of a million tokens to show
// one picture once.
//
// Grok is deliberately untouched. It shares this translator, but the tool is
// added only in the Codex proxy's own request builder and xAI publishes no such
// built-in, so a Grok session behaves exactly as it did.

// ImageInfo is what the backend said about a picture it drew.
type ImageInfo struct {
	// Action is "generate" or "edit". Carried because it is the only confirmation
	// that an edit was understood as an edit rather than quietly redrawn from
	// scratch, which looks identical in the result and not at all in the answer.
	Action string
	// RevisedPrompt is what the backend actually drew from, which is never quite
	// what was asked: it rewrites the prompt first. When a picture comes out
	// wrong this usually says why, so it is shown rather than dropped.
	RevisedPrompt string
	Size          string
	Format        string // "png" unless the backend says otherwise
}

// ImageSaver stores a generated image and returns the absolute path it landed
// at. It is an interface so this package never learns where kunai keeps files;
// the server supplies one at startup.
type ImageSaver interface {
	SaveImage(data []byte, info ImageInfo) (path string, err error)
}

var (
	saverMu    sync.RWMutex
	imageSaver ImageSaver
)

// SetImageSaver turns picture-making on. Until one is set the tool is never
// offered, so a build with no somewhere-to-put-it behaves exactly as before:
// the capability is gated on being able to deliver it, not on a separate flag
// that could disagree.
func SetImageSaver(s ImageSaver) {
	saverMu.Lock()
	imageSaver = s
	saverMu.Unlock()
}

func currentSaver() ImageSaver {
	saverMu.RLock()
	defer saverMu.RUnlock()
	return imageSaver
}

// injectImageTool adds the built-in image tool to an already-translated request.
//
// It runs AFTER ConvertClaudeRequestToCodex rather than inside it, and that is
// load-bearing: the translator rewrites any tool whose type is not "function"
// into one (see the tool loop in translate_request.go), so a built-in added
// before translation would arrive as a nameless function and be rejected. Doing
// it here also keeps the shared translator, and therefore Grok, untouched.
//
// The tool is added to every request, which was measured rather than assumed to
// be safe: a request carrying image_generation alongside a normal Read/Write/
// Bash/Glob/Grep toolset still drew the picture, and a request that does not ask
// for one is unaffected beyond a few tokens of tool declaration.
func injectImageTool(body []byte) []byte {
	if currentSaver() == nil {
		return body
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.IsArray() {
		for _, t := range tools.Array() {
			if t.Get("type").String() == "image_generation" {
				return body // already there; never declare it twice
			}
		}
	}
	out, err := sjson.SetRawBytes(body, "tools.-1", []byte(`{"type":"image_generation"}`))
	if err != nil {
		return body
	}
	// A tools array that did not exist starts one, and an empty one is dropped
	// downstream by dropOrphanToolChoice along with its tool_choice; setting
	// tool_choice here would fight that, so it is left alone.
	return out
}

// appendImageResult saves a finished picture and announces it in the stream.
//
// The announcement is a plain text block holding a markdown image, because that
// is what the client already knows how to show. It closes any open text block
// first so the picture does not land inside a half-written sentence.
func appendImageResult(output []byte, params *ConvertCodexResponseToClaudeParams, item gjson.Result) []byte {
	saver := currentSaver()
	if saver == nil {
		return output
	}
	b64 := item.Get("result").String()
	if b64 == "" {
		return output
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		log.Printf("codex image: result was not valid base64: %v", err)
		return output
	}
	info := ImageInfo{
		Action:        item.Get("action").String(),
		RevisedPrompt: item.Get("revised_prompt").String(),
		Size:          item.Get("size").String(),
		Format:        item.Get("output_format").String(),
	}
	path, err := saver.SaveImage(data, info)
	if err != nil {
		// The picture exists and we cannot put it anywhere. Say so in the reply
		// rather than only in the log: a turn that silently produced nothing is
		// indistinguishable from a model that declined to draw.
		log.Printf("codex image: %v", err)
		return appendImageText(output, params, "\n\n_(an image was generated but could not be saved: "+err.Error()+")_\n\n")
	}

	alt := info.RevisedPrompt
	if alt == "" {
		alt = "generated image"
	}
	return appendImageText(output, params, "\n\n!["+markdownAlt(alt)+"]("+path+")\n\n")
}

// appendImageText emits one self-contained text block carrying the markdown.
//
// A block of its own, rather than a delta into whatever was open, so the image
// cannot be spliced into the middle of a sentence the model was still writing.
func appendImageText(output []byte, params *ConvertCodexResponseToClaudeParams, text string) []byte {
	output = append(output, finalizeCodexThinkingBlock(params)...)
	output = append(output, stopCodexTextBlock(params)...)
	output = append(output, startCodexTextBlock(params)...)

	delta := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
	delta, _ = sjson.SetBytes(delta, "index", params.BlockIndex)
	delta, _ = sjson.SetBytes(delta, "delta.text", text)
	output = AppendSSEEventBytes(output, "content_block_delta", delta, 2)

	// HasTextDelta tells output_item.done for the message not to re-emit the
	// whole text; the picture counts, or the reply would arrive twice.
	params.HasTextDelta = true
	return append(output, stopCodexTextBlock(params)...)
}

// markdownAlt keeps a revised prompt from breaking out of the alt text. The
// prompt is model-written prose and routinely contains brackets.
func markdownAlt(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '[', ']':
			out = append(out, ' ')
		case '\n', '\r':
			out = append(out, ' ')
		default:
			out = append(out, r)
		}
	}
	// Long enough to identify the picture, short enough not to be the reply.
	const limit = 120
	if len(out) > limit {
		return string(out[:limit]) + "…"
	}
	return string(out)
}
