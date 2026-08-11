package codex

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

type fakeSaver struct {
	path string
	err  error
	got  []byte
	info ImageInfo
}

func (f *fakeSaver) SaveImage(data []byte, info ImageInfo) (string, error) {
	f.got, f.info = data, info
	return f.path, f.err
}

// withSaver installs a saver for one test and takes it away again, since it is
// process-wide state.
func withSaver(t *testing.T, s ImageSaver) {
	t.Helper()
	SetImageSaver(s)
	t.Cleanup(func() { SetImageSaver(nil) })
}

func TestImageToolIsOfferedOnlyWhenThereIsSomewhereToPutTheResult(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","tools":[{"type":"function","name":"Read"}]}`)

	// No saver: the capability is gated on being able to deliver it, so nothing
	// is added and a build with no data dir behaves exactly as before.
	if got := injectImageTool(body); string(got) != string(body) {
		t.Errorf("tool was offered with no saver:\n%s", got)
	}

	withSaver(t, &fakeSaver{path: "/tmp/x.png"})
	out := injectImageTool(body)
	tools := gjson.GetBytes(out, "tools").Array()
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d: %s", len(tools), out)
	}
	// The existing function tool must survive untouched beside it.
	if tools[0].Get("name").String() != "Read" {
		t.Errorf("the original tool was disturbed: %s", tools[0].Raw)
	}
	if tools[1].Get("type").String() != "image_generation" {
		t.Errorf("want an image_generation tool, got %s", tools[1].Raw)
	}
}

func TestImageToolIsNeverDeclaredTwice(t *testing.T) {
	withSaver(t, &fakeSaver{path: "/tmp/x.png"})
	once := injectImageTool([]byte(`{"tools":[{"type":"function","name":"Read"}]}`))
	twice := injectImageTool(once)
	if n := len(gjson.GetBytes(twice, "tools").Array()); n != 2 {
		t.Errorf("re-injecting produced %d tools, want 2: %s", n, twice)
	}
}

func TestImageToolStartsAToolsArrayWhenThereIsNone(t *testing.T) {
	withSaver(t, &fakeSaver{path: "/tmp/x.png"})
	out := injectImageTool([]byte(`{"model":"gpt-5.5"}`))
	if n := len(gjson.GetBytes(out, "tools").Array()); n != 1 {
		t.Errorf("want 1 tool, got %d: %s", n, out)
	}
}

// The picture arrives as base64 on the finished item, and must leave as markdown
// pointing at wherever it was saved: that is the whole delivery mechanism, since
// an Anthropic assistant message cannot carry an image.
func TestAGeneratedImageIsSavedAndAnnouncedAsMarkdown(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nnot-really-a-png")
	saver := &fakeSaver{path: "/data/generated-images/pic.png"}
	withSaver(t, saver)

	item := gjson.Parse(`{"type":"image_generation_call","action":"edit","revised_prompt":"a red circle","size":"1024x1024","output_format":"png","result":"` +
		base64.StdEncoding.EncodeToString(png) + `"}`)
	params := &ConvertCodexResponseToClaudeParams{}
	out := string(appendImageResult(nil, params, item))

	if string(saver.got) != string(png) {
		t.Errorf("saved %q, want the decoded png", saver.got)
	}
	if saver.info.Action != "edit" || saver.info.RevisedPrompt != "a red circle" {
		t.Errorf("metadata lost: %+v", saver.info)
	}
	if !strings.Contains(out, "![a red circle](/data/generated-images/pic.png)") {
		t.Errorf("no markdown image in the stream:\n%s", out)
	}
	for _, want := range []string{"content_block_start", "content_block_delta", "content_block_stop"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
	// Claiming the text was delivered stops output_item.done re-emitting the
	// whole message, which would send the reply twice.
	if !params.HasTextDelta {
		t.Error("HasTextDelta was not set, so the message will be emitted again")
	}
}

// A picture that cannot be stored must still be reported. A turn that silently
// produced nothing is indistinguishable from a model that declined to draw.
func TestAnUnsavableImageSaysSoInTheReply(t *testing.T) {
	withSaver(t, &fakeSaver{err: errNoSpace{}})
	item := gjson.Parse(`{"type":"image_generation_call","result":"` +
		base64.StdEncoding.EncodeToString([]byte("x")) + `"}`)
	out := string(appendImageResult(nil, &ConvertCodexResponseToClaudeParams{}, item))
	if !strings.Contains(out, "could not be saved") || !strings.Contains(out, "disk full") {
		t.Errorf("the failure was not reported to the reader:\n%s", out)
	}
}

type errNoSpace struct{}

func (errNoSpace) Error() string { return "disk full" }

func TestAltTextCannotBreakOutOfTheMarkdown(t *testing.T) {
	saver := &fakeSaver{path: "/tmp/pic.png"}
	withSaver(t, saver)
	// A revised prompt is model-written prose and routinely carries brackets and
	// newlines; unescaped, either one ends the alt text early and the rest of the
	// prompt lands in the reply as broken markup.
	item := gjson.Parse(`{"type":"image_generation_call","revised_prompt":"a [red] circle\nwith a note","result":"` +
		base64.StdEncoding.EncodeToString([]byte("x")) + `"}`)
	out := string(appendImageResult(nil, &ConvertCodexResponseToClaudeParams{}, item))
	if strings.Contains(out, `[red]`) {
		t.Errorf("brackets survived into the alt text:\n%s", out)
	}
	if !strings.Contains(out, "(/tmp/pic.png)") {
		t.Errorf("the link was broken by the alt text:\n%s", out)
	}
}
