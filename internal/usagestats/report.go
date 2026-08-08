package usagestats

import (
	"sort"
	"time"
)

// The report is built once per scan over EVERY day the index holds, and the
// client slices it to the window it is showing. The whole corpus reduces to a few
// thousand (day, model) rows, so keeping all of it costs nothing and means
// switching between 7, 30 and 90 days is instant rather than another pass over
// the transcripts.

// Report is the whole history, day by day and model by model.
type Report struct {
	Days      []DayStat   `json:"days"`
	Models    []ModelStat `json:"models"`
	Files     int         `json:"files"`
	ScannedAt int64       `json:"scanned_at"`
}

// DayStat is one calendar day, split by model so the client can group it by
// model or by agent without another server round trip.
type DayStat struct {
	Day    string      `json:"day"`
	Models []ModelStat `json:"models"`
}

// ModelStat is what one model did, and what it would have cost.
type ModelStat struct {
	Model string `json:"model"`
	Agent string `json:"agent"`
	// Priced is false when no rate is known for this model. Cost is then 0 and
	// must be presented as "not priced", never as "free".
	Priced  bool    `json:"priced"`
	Cost    float64 `json:"cost"`
	Savings float64 `json:"savings"`
	Tokens
}

// build turns the per-file index into the report.
func build(files map[string]*fileState, tbl *Table) *Report {
	byDay := map[string]map[string]Tokens{}
	for _, st := range files {
		for _, row := range st.Rows {
			models := byDay[row.Day]
			if models == nil {
				models = map[string]Tokens{}
				byDay[row.Day] = models
			}
			t := models[row.Model]
			t.Add(row.Tokens)
			models[row.Model] = t
		}
	}

	total := map[string]Tokens{}
	days := make([]DayStat, 0, len(byDay))
	for day, models := range byDay {
		d := DayStat{Day: day, Models: statsOf(models, tbl)}
		days = append(days, d)
		for model, t := range models {
			cur := total[model]
			cur.Add(t)
			total[model] = cur
		}
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Day < days[j].Day })

	return &Report{
		Days:      days,
		Models:    statsOf(total, tbl),
		Files:     len(files),
		ScannedAt: time.Now().UnixMilli(),
	}
}

// statsOf prices a set of models, biggest spender first so the client can render
// a breakdown without sorting it again.
func statsOf(models map[string]Tokens, tbl *Table) []ModelStat {
	out := make([]ModelStat, 0, len(models))
	for model, t := range models {
		cost, priced := tbl.Cost(model, t)
		out = append(out, ModelStat{
			Model:   model,
			Agent:   Agent(model),
			Priced:  priced,
			Cost:    cost,
			Savings: tbl.CacheSaving(model, t),
			Tokens:  t,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Tokens.Total() > out[j].Tokens.Total()
	})
	return out
}
