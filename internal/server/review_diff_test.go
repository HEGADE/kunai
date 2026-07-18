package server

import (
	"strings"
	"testing"
)

// A multi-file diff covering the shapes that matter: a modification, a new file,
// a deletion, a rename, and a binary. Captured from real `git diff` output.
const sampleDiff = `diff --git a/src/app.ts b/src/app.ts
index 83db48f..bf3d2a0 100644
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,4 +1,4 @@
 import x from 'y'
-const a = 1
+const a = 2
+const b = 3
 export { a }
diff --git a/src/new.ts b/src/new.ts
new file mode 100644
index 0000000..a1b2c3d
--- /dev/null
+++ b/src/new.ts
@@ -0,0 +1,2 @@
+export const hi = 1
+export const bye = 2
diff --git a/src/gone.ts b/src/gone.ts
deleted file mode 100644
index abc1234..0000000
--- a/src/gone.ts
+++ /dev/null
@@ -1,1 +0,0 @@
-const gone = true
diff --git a/old/name.ts b/new/name.ts
similarity index 90%
rename from old/name.ts
rename to new/name.ts
index 111..222 100644
--- a/old/name.ts
+++ b/new/name.ts
@@ -3,3 +3,3 @@ ctx
 keep
-was
+now
 keep2
diff --git a/logo.png b/logo.png
index 333..444 100644
Binary files a/logo.png and b/logo.png differ
`

func TestParseUnifiedDiff(t *testing.T) {
	files := parseUnifiedDiff([]byte(sampleDiff))
	if len(files) != 5 {
		t.Fatalf("got %d files, want 5", len(files))
	}

	// 1. modification: path, status, and a real +/- with line numbers.
	m := files[0]
	if m.Path != "src/app.ts" || m.Status != "modified" {
		t.Errorf("file 0 = %q/%q, want src/app.ts/modified", m.Path, m.Status)
	}
	var adds, dels, ctx, hunks int
	for _, l := range m.Lines {
		switch l.Kind {
		case "add":
			adds++
		case "del":
			dels++
		case "ctx":
			ctx++
		case "hunk":
			hunks++
		}
	}
	if adds != 2 || dels != 1 || ctx != 2 || hunks != 1 {
		t.Errorf("file 0 rows = +%d/-%d ctx%d hunk%d, want +2/-1 ctx2 hunk1", adds, dels, ctx, hunks)
	}
	// The first add lands on new line 2 (after the kept import on line 1).
	for _, l := range m.Lines {
		if l.Kind == "add" && l.Text == "const a = 2" && l.New != 2 {
			t.Errorf("add 'const a = 2' New = %d, want 2", l.New)
		}
	}

	// 2. added file, from /dev/null.
	if files[1].Path != "src/new.ts" || files[1].Status != "added" {
		t.Errorf("file 1 = %q/%q, want src/new.ts/added", files[1].Path, files[1].Status)
	}

	// 3. deleted file keeps its name from the --- side.
	if files[2].Path != "src/gone.ts" || files[2].Status != "deleted" {
		t.Errorf("file 2 = %q/%q, want src/gone.ts/deleted", files[2].Path, files[2].Status)
	}

	// 4. rename records both ends.
	r := files[3]
	if r.Status != "renamed" || r.Old != "old/name.ts" || r.Path != "new/name.ts" {
		t.Errorf("file 3 = %q old=%q path=%q, want renamed old/name.ts -> new/name.ts", r.Status, r.Old, r.Path)
	}

	// 5. binary carries no rows.
	b := files[4]
	if !b.Binary || b.Path != "logo.png" {
		t.Errorf("file 4 binary=%v path=%q, want true/logo.png", b.Binary, b.Path)
	}
	if len(b.Lines) != 0 {
		t.Errorf("binary file has %d rows, want 0", len(b.Lines))
	}
}

func TestParseUnifiedDiffRowCap(t *testing.T) {
	var big strings.Builder
	big.WriteString("diff --git a/big.txt b/big.txt\n--- a/big.txt\n+++ b/big.txt\n@@ -1,1 +1,100000 @@\n")
	for i := 0; i < maxDiffLines+500; i++ {
		big.WriteString("+line\n")
	}
	files := parseUnifiedDiff([]byte(big.String()))
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	if !files[0].Truncated {
		t.Error("want Truncated set past the row cap")
	}
	if len(files[0].Lines) > maxDiffLines+1 {
		t.Errorf("kept %d rows, want <= %d", len(files[0].Lines), maxDiffLines+1)
	}
}
