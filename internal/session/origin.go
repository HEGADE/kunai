package session

import "errors"

// Who sent a thing, and what that changes.
//
// A session used to have exactly one driver, so nothing needed to say where a
// prompt came from. A shared link breaks that: two people can be typing into the
// same conversation, only one of them owns the machine, and the actions that end
// or discard work must not be available to the visitor.
//
// The marker rides on the events too, because an owner reading their own log has
// to be able to tell which turns were theirs. That matters most at a permission
// ask: approving from a lock-screen notification with nothing but a tool name is
// how a guest's request gets waved through as if it were your own.

// Origin is who caused something.
type Origin string

const (
	// FromOwner is anyone reaching kunai over the tailnet: its owner.
	FromOwner Origin = ""
	// FromGuest is someone holding a share link. Never trusted with anything that
	// ends work, and always visible as such in the log.
	FromGuest Origin = "guest"
)

// ErrNotYours is returned when a guest tries to stop or discard work they did not
// start. Worded for the person who will read it, who is not the owner.
var ErrNotYours = errors.New("that was started by the session's owner, so only they can stop it")

// PromptFrom sends a turn and records who sent it.
func (s *Session) PromptFrom(text string, from Origin) error {
	return s.prompt(&queuedPrompt{Text: text, from: from})
}

// PromptWithFilesFrom is PromptFrom carrying attachments, for a guest who sent a
// picture. content is the model-facing content the caller built from them; atts
// is the metadata the conversation shows. Kept beside PromptFrom rather than
// widening it, so every existing caller keeps saying exactly what it means.
func (s *Session) PromptWithFilesFrom(text string, content any, atts []Attachment, from Origin) error {
	return s.prompt(&queuedPrompt{Text: text, Attachments: atts, content: content, from: from})
}

// InterruptFrom stops the current turn, but only if from is entitled to.
//
// The plain Interrupt is deliberately not exposed to a guest. It calls
// dropQueueLocked and stopLoopLocked before it touches the driver, so it ends any
// running loop and discards every prompt the owner had queued. Handing that to a
// visitor means one tap can end an overnight run. A guest may stop what a guest
// started; the owner may stop anything.
func (s *Session) InterruptFrom(from Origin) error {
	if from == FromOwner {
		return s.Interrupt()
	}
	s.mu.Lock()
	mine := s.turnFrom == from && s.state == StateRunning
	s.mu.Unlock()
	if !mine {
		return ErrNotYours
	}
	return s.Interrupt()
}

// CancelQueuedFrom drops a queued prompt, if from is the one who queued it. The
// queue ids are broadcast to everyone attached, so without this check a guest
// could cancel the owner's prompts by reading the ids off their own stream.
func (s *Session) CancelQueuedFrom(id string, from Origin) error {
	if from == FromOwner {
		s.CancelQueued(id)
		return nil
	}
	s.mu.Lock()
	found, mine := false, false
	for _, q := range s.queue {
		if q.ID == id {
			found, mine = true, q.from == from
			break
		}
	}
	s.mu.Unlock()
	if !found {
		return nil // already ran or already cancelled; nothing to report
	}
	if !mine {
		return ErrNotYours
	}
	s.CancelQueued(id)
	return nil
}
