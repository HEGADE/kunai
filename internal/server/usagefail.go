package server

// What a quota poll does when it cannot get an answer.
//
// The Codex and Grok quota caches both cached only SUCCESS. A provider whose
// login had gone stale therefore had every caller repeat the request, and every
// repeat wrote a log line, so a single dead credential produced a steady drip
// forever: on this machine roughly half of everything in the journal, in the log
// you open precisely when something else is wrong.
//
// Two things follow, and they are separate. A failure has to be remembered, so
// the next caller waits instead of asking a third party that has already said no.
// And it has to be reported once, because "this login is dead" is one fact, not a
// fact per minute. Only a CHANGE in what the failure says is news.

import (
	"context"
	"log"
	"time"
)

// usageFailure is the negative half of a quota cache: how long to stop asking,
// and what the last failure said so an unchanged one stays quiet.
//
// The zero value is "no failure recorded", which is what a cache starts as and
// what a success returns it to.
type usageFailure struct {
	until time.Time
	last  string
}

// holding reports whether a previous failure is still standing, in which case the
// caller should return what it has rather than repeat the request.
func (f *usageFailure) holding(now time.Time) bool {
	return !f.until.IsZero() && now.Before(f.until)
}

// note records a failure and reports whether it is worth logging: the first one,
// or a different message than last time. The back-off matches the success TTL, so
// a poll costs at most one request per period whether it works or not, and a
// login fixed at the console shows up within one period rather than being punished
// for having failed.
func (f *usageFailure) note(now time.Time, ttl time.Duration, msg string) bool {
	f.until = now.Add(ttl)
	if f.last == msg {
		return false
	}
	f.last = msg
	return true
}

// clear forgets the failure after a success, so the next one is reported rather
// than silently swallowed as a repeat of something long resolved.
func (f *usageFailure) clear() {
	f.until, f.last = time.Time{}, ""
}

// report records a failure and logs it only if it is news.
func (f *usageFailure) report(now time.Time, ttl time.Duration, what, msg string) {
	if f.note(now, ttl, msg) {
		log.Printf("%s: %s (not asking again for %s)", what, msg, ttl)
	}
}

// providerUsageFailTTL is how long a Codex or Grok quota failure is held.
//
// Deliberately the full usageTTL rather than the usageFailTTL the Claude poll
// uses. That one is 10 seconds because re-running it costs one cheap LOCAL CLI
// invocation, so a blip should clear fast. These two are HTTPS requests to a
// third party, and retrying a credential that has already been refused six times
// a minute is worse than the once-a-minute drip this is fixing.
const providerUsageFailTTL = usageTTL

// reason is the last failure message, or "" when there is none.
//
// It exists because the message was being thrown away one layer before anyone
// could read it. Both provider caches held a precise, actionable sentence -- "the
// Codex login has expired; sign in to Codex again" -- reported it to the log, and
// then returned a bare nil, so the handler could only answer with a generic
// "usage not available for this provider" and the dashboard could only print "no
// quota". That is indistinguishable from "this provider has no quota to show",
// which is the wrong conclusion and the one a reader will reach.
func (f *usageFailure) reason() string { return f.last }

// abandoned reports whether a poll failed because WE hung up rather than because
// the provider said no.
//
// The quota fetch runs on the HTTP request's context, so a client that navigates
// away, unmounts, or supersedes its own poll cancels it mid-flight. That is not
// a fault and there is nothing to act on, but it arrived at the failure path all
// the same: it was recorded, which parked the quota for a whole minute over a
// request nobody was waiting for any more, and once failures became visible it
// printed `context canceled` on the dashboard as though the account were broken.
//
// ctx.Err() is what tells the two apart, and it has to be the CONTEXT rather
// than the error: the fetch's own 8-second client timeout also surfaces as a
// deadline error, and that one is a real failure worth remembering. Same
// distinction the stream translator makes for a cancelled turn.
func abandoned(ctx context.Context) bool { return ctx.Err() != nil }
