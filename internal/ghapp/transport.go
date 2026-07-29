package ghapp

// The one place an HTTP request to GitHub is made.
//
// Every call in this package goes through do(), so the things that must be true
// of all of them are true in one place: the right Authorization header for the
// hop being made, the API version pin, a body decoded only on success, and a
// failure that says what GitHub actually complained about instead of "400".
//
// The auth mode is a parameter rather than something do() works out, because the
// two hops are genuinely different and confusing them is the mistake worth making
// impossible: an App JWT cannot read a repository, and an installation token
// cannot list installations. Naming the mode at each call site keeps that
// visible.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// authMode selects which credential a request carries.
type authMode int

const (
	// authApp signs as the App itself: only /app/... endpoints accept this.
	authApp authMode = iota
	// authInstallation carries a token already minted for an installation. The
	// token travels in the request rather than being fetched here, because the
	// caller knows which installation it means and do() must not guess.
	authInstallation
)

// apiVersion pins the REST API's dated version. GitHub changes behaviour behind
// this header, so pinning means a change on their side is something we adopt
// deliberately rather than discover through a broken review.
const apiVersion = "2022-11-28"

// do performs one API call, decoding JSON into out when out is non-nil.
//
// token is used only when mode is authInstallation; for authApp a fresh JWT is
// signed, which is cheap and avoids caching a credential whose whole purpose is
// to be short-lived.
func (a *App) do(ctx context.Context, method, path string, mode authMode, body any, out any) error {
	return a.doAuthed(ctx, method, path, mode, "", body, out)
}

// doAuthed is do() with an explicit installation token, for the calls that act on
// a repository.
func (a *App) doAuthed(ctx context.Context, method, path string, mode authMode, token string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request to GitHub: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.base+path, reader)
	if err != nil {
		return fmt.Errorf("building the request to GitHub: %w", err)
	}

	switch mode {
	case authApp:
		jwt, err := a.creds.signJWT(a.now())
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
	case authInstallation:
		if token == "" {
			return fmt.Errorf("internal: %s %s needs an installation token and was given none", method, path)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := a.http.Do(req)
	if err != nil {
		// The URL is safe to surface, the headers are not, and net/http never puts
		// them in an error. Nothing further is added here for that reason.
		return fmt.Errorf("calling GitHub: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return apiError(res)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("reading GitHub's reply to %s %s: %w", method, path, err)
	}
	return nil
}

// APIError is a non-2xx from GitHub, carrying the status and whatever GitHub said
// about it. Callers match on Status rather than parsing the message.
type APIError struct {
	Status  int
	Message string
	// Errors are GitHub's per-field complaints, which is where the real reason
	// lives for a rejected review: "pull_request_review_thread.line must be part
	// of the diff" is in here, while Message says only "Validation Failed".
	Errors []string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "no message"
	}
	if len(e.Errors) > 0 {
		msg += " (" + strings.Join(e.Errors, "; ") + ")"
	}
	return fmt.Sprintf("GitHub returned %d: %s", e.Status, msg)
}

// apiError reads GitHub's error body into an APIError. A body that is not the
// documented shape still produces a usable error rather than swallowing the
// status, because an unparseable failure is exactly when the status matters most.
func apiError(res *http.Response) error {
	var payload struct {
		Message string `json:"message"`
		Errors  []struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	_ = json.Unmarshal(raw, &payload)

	err := &APIError{Status: res.StatusCode, Message: payload.Message}
	for _, e := range payload.Errors {
		switch {
		case e.Message != "":
			err.Errors = append(err.Errors, e.Message)
		case e.Field != "":
			err.Errors = append(err.Errors, strings.TrimSpace(e.Resource+"."+e.Field+" "+e.Code))
		}
	}
	if err.Message == "" && len(raw) > 0 {
		err.Message = strings.TrimSpace(string(raw))
	}
	return err
}
