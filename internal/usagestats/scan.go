// Package usagestats answers "what did all of this cost" from the transcripts
// that are already on disk.
//
// The whole feature rests on one fact: every assistant message the CLI writes to
// `<configDir>/projects/*/<id>.jsonl` carries its own `usage` block and the model
// that produced it. So the answer is computable RETROACTIVELY, over history that
// predates this code, with nothing needing to have been recorded in advance. That
// is why this reads transcripts rather than instrumenting the live session: a
// meter that only counts from the day you install it is not the number anyone
// wants to see.
//
// The corpus is the constraint. On the machine this was written for it is ~1.5GB
// across ~145 files spread over seven accounts, so parsing it per request is out
// of the question -- `history.go` already learned that lesson at 69MB. Two things
// make it cheap. A transcript is APPEND-ONLY, so a rescan seeks to where the last
// one stopped and reads only what has arrived since; and the result is aggregated
// into (day, model) buckets as it goes, so what is kept is a few hundred rows per
// file rather than anything proportional to its size.
package usagestats

import (
	"encoding/json"
	"io"
	"os"
	"time"
)

// Key is one bucket of spend: a day, and the model that spent it.
//
// Day is a local calendar date (YYYY-MM-DD) rather than UTC, because the question
// is "what did I spend today" and today is a thing that happens where the person
// is standing.
type Key struct {
	Day   string `json:"day"`
	Model string `json:"model"`
}

// Tokens is what one bucket used.
//
// The two cache-write fields are kept apart because they are priced apart: a
// 5-minute write is 1.25x the input rate and a 1-hour write is 2x. Summing them
// into one number, which the transcript's own `cache_creation_input_tokens` does,
// would quietly under-bill every long-lived cache.
type Tokens struct {
	Input        int64 `json:"in"`
	CacheWrite5m int64 `json:"w5"`
	CacheWrite1h int64 `json:"w1"`
	CacheRead    int64 `json:"r"`
	Output       int64 `json:"out"`
	Responses    int64 `json:"n"`
}

// Add folds one bucket into another.
func (t *Tokens) Add(o Tokens) {
	t.Input += o.Input
	t.CacheWrite5m += o.CacheWrite5m
	t.CacheWrite1h += o.CacheWrite1h
	t.CacheRead += o.CacheRead
	t.Output += o.Output
	t.Responses += o.Responses
}

// Total is every token the bucket touched, cached or not. This is the "processed
// tokens" headline, and it is deliberately enormous: a long turn re-reads its
// whole context on every tool call, so cache reads dominate by an order of
// magnitude. That is the honest number, and it is exactly why the split matters.
func (t Tokens) Total() int64 {
	return t.Input + t.CacheWrite5m + t.CacheWrite1h + t.CacheRead + t.Output
}

// Row is a bucket in the form it persists as. A map cannot be a JSON key here
// (Key is a struct), and a flat row is also what the aggregator wants.
type Row struct {
	Key
	Tokens
}

// record is the sliver of a transcript line this cares about.
//
// Everything else -- content blocks, thinking signatures, tool results, the
// megabyte of file text a Read produced -- is left undeclared so encoding/json
// skips it without allocating. That is what keeps a 175MB transcript from
// becoming 175MB of garbage.
type record struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CacheCreate   int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheCreation *struct {
				E5m int64 `json:"ephemeral_5m_input_tokens"`
				E1h int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
}

// ScanFile reads a transcript from a byte offset and returns the spend it found
// plus the offset to resume from next time.
//
// The returned offset is always the end of the last COMPLETE record. A transcript
// being appended to while this runs will usually end mid-line, and stopping short
// of that partial line is what makes the resume exact rather than approximately
// right: the next scan re-reads those bytes once they are whole.
func ScanFile(path string, from int64) (map[Key]Tokens, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, from, err
	}
	defer f.Close()

	if from > 0 {
		if _, err := f.Seek(from, io.SeekStart); err != nil {
			return nil, 0, err
		}
	}

	buckets := map[Key]Tokens{}
	dec := json.NewDecoder(f)
	off := from
	for {
		var rec record
		if err := dec.Decode(&rec); err != nil {
			// EOF is the normal end. Anything else means a malformed or half-written
			// record, and the answer is the same: stop, keep the offset of the last
			// good one, and try again next scan. A transcript is not corrupt just
			// because it is being written to.
			break
		}
		off = from + dec.InputOffset()

		u := rec.Message.Usage
		// `<synthetic>` is the CLI's own stand-in model for a message it wrote
		// itself (an API error surfaced as an assistant turn). It bought nothing
		// and cost nothing, so counting it would inflate the response count and
		// then show up in the UI as an unpriced model nobody has ever heard of.
		if u == nil || rec.Message.Model == "" || rec.Message.Model == "<synthetic>" {
			continue
		}
		day, ok := localDay(rec.Timestamp)
		if !ok {
			continue
		}

		w5, w1 := u.CacheCreate, int64(0)
		if c := u.CacheCreation; c != nil && c.E5m+c.E1h > 0 {
			w5, w1 = c.E5m, c.E1h
		}
		k := Key{Day: day, Model: rec.Message.Model}
		t := buckets[k]
		t.Add(Tokens{
			Input:        u.Input,
			CacheWrite5m: w5,
			CacheWrite1h: w1,
			CacheRead:    u.CacheRead,
			Output:       u.Output,
			Responses:    1,
		})
		buckets[k] = t
	}
	return buckets, off, nil
}

// localDay turns a transcript timestamp into the calendar day it happened on,
// here.
func localDay(ts string) (string, bool) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "", false
	}
	return t.Local().Format("2006-01-02"), true
}
