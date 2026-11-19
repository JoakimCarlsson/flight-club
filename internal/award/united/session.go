package united

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/award"
	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// errSignInNeeded is returned when united.com asks for a sign-in that
// cannot be done without a person: no account is configured, or the site
// wants a verification code it sent the member. Running SignIn with a
// CodeFunc again fixes it.
var errSignInNeeded = errors.New("united: sign-in needed")

// Account is the MileagePlus member Search signs in as.
type Account struct {
	// Email is the member's MileagePlus number or sign-in email. The site
	// refuses emails with a "+" in them, so the MileagePlus number is the
	// safer choice.
	Email string
	// Password is the member's password.
	Password string
	// SessionFile is where the signed-in session's cookies are kept;
	// empty keeps them under the user cache directory.
	SessionFile string
}

// AccountFromEnv reads the account from UNITED_EMAIL, UNITED_PASSWORD and
// UNITED_SESSION_FILE. It returns false when the email or the password is
// unset, in which case the program should not be offered.
func AccountFromEnv() (Account, bool) {
	a := Account{
		Email:       os.Getenv("UNITED_EMAIL"),
		Password:    os.Getenv("UNITED_PASSWORD"),
		SessionFile: os.Getenv("UNITED_SESSION_FILE"),
	}
	return a, a.Email != "" && a.Password != ""
}

// sessionPath is where the account's session is kept: SessionFile, or
// flight-club/united-session.json under the user cache directory. It is
// empty when neither is known, which keeps no session.
func (a Account) sessionPath() string {
	if a.SessionFile != "" {
		return a.SessionFile
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "flight-club", "united-session.json")
}

// session is the signed-in session's cookies, kept in a JSON file so it
// outlives the process. It also remembers that the site wanted a person
// to sign in, so searches do not keep asking the site for codes, until the
// file changes. It is safe for concurrent use.
type session struct {
	path string
	mu   sync.Mutex
	// stuck is set when a sign-in needed a person.
	stuck bool
	// stuckAt is the file's modification time when stuck was set.
	stuckAt time.Time
}

// modTime is the session file's modification time, zero when it has none.
func (s *session) modTime() time.Time {
	info, err := os.Stat(s.path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// markStuck remembers that signing in needs a person until the session
// file changes.
func (s *session) markStuck() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stuck, s.stuckAt = true, s.modTime()
}

// isStuck reports whether signing in needed a person and the session file
// has not changed since.
func (s *session) isStuck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stuck && !s.modTime().Equal(s.stuckAt) {
		s.stuck = false
	}
	return s.stuck
}

// load returns the saved cookies, or nil when there are none or the file
// cannot be read.
func (s *session) load() []browser.Cookie {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var cookies []browser.Cookie
	if json.Unmarshal(data, &cookies) != nil {
		return nil
	}
	return cookies
}

// save replaces the saved cookies with cookies, writing a new file and
// renaming it over the old one so a reader never sees half a file. It
// keeps nothing when cookies is empty and returns an error when the file
// cannot be written.
func (s *session) save(cookies []browser.Cookie) error {
	if s.path == "" || len(cookies) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(cookies, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("united: session file: %w", err)
	}
	tmp := s.path + ".new"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("united: session file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("united: session file: %w", err)
	}
	return nil
}

// CodeFunc returns the verification code united.com sent the member, such
// as by asking the person at the terminal for it.
type CodeFunc func(ctx context.Context) (string, error)

// SignIn signs the member in on united.com, answering a verification code
// with code if the site asks for one and asking it to remember the member,
// then saves the session to the account's session file, so later searches
// stay signed in. It returns an error when the sign-in is refused or does
// not finish, or the session cannot be saved; it returns the path the
// session was saved to.
func (p *Program) SignIn(ctx context.Context, code CodeFunc) (string, error) {
	q := award.Query{
		Origin:      "SFO",
		Destination: "EWR",
		Date:        time.Now().AddDate(0, 0, 30),
		Passengers:  1,
	}
	body, captured, err := p.collect(ctx, q, code)
	if err != nil {
		return "", err
	}
	if body == nil {
		return "", errors.New("united: sign-in did not finish")
	}
	if err := p.session.save(captured.Cookies); err != nil {
		return "", err
	}
	return p.session.path, nil
}

// Timings of the sign-in and search: how long the results page is watched
// for the search answer or the sign-in dialog, how long a form field is
// given to appear, and how long a submitted step is given to lead
// anywhere.
const (
	answerWait = 60 * time.Second
	formWait   = 20 * time.Second
	submitWait = 60 * time.Second
	pollDelay  = time.Second
	// reloadAfter is how long an answer the page already fetched is
	// waited for before the page is reloaded to fetch it again.
	reloadAfter = 15 * time.Second
)

// Selectors of the sign-in dialog the results page opens for a visitor
// who is not signed in.
const (
	emailSelector    = `#MPIDEmailField`
	passwordSelector = `[role=dialog] input[type=password]`
	rememberSelector = `[role=dialog] input[type=checkbox][name=rememberMe]`
	codeSelector     = `[role=dialog] input[autocomplete=one-time-code], ` +
		`[role=dialog] input[name*=ode i], [role=dialog] input[id*=ode i], ` +
		`[role=dialog] input[name*=otp i], [role=dialog] input[id*=otp i]`
	anyFieldSelector = `[role=dialog] input:not([type=password])` +
		`:not([type=checkbox]):not([type=hidden])`
)

// awaitAnswer watches page, freshly opened on the results page, until
// has reports the search answer was captured, signing account in with
// signIn whenever the sign-in dialog shows. It returns what signIn
// returns, nil when the answer came, and an error when neither happened
// in time.
func awaitAnswer(
	ctx context.Context,
	page *browser.Page,
	account Account,
	code CodeFunc,
	has func() bool,
) error {
	signedIn, reloaded := false, false
	start := time.Now()
	deadline := start.Add(answerWait)
	for time.Now().Before(deadline) {
		if has() {
			return nil
		}
		if !reloaded && time.Since(start) > reloadAfter && missed(page) {
			reloaded = true
			_ = page.Reload()
		}
		if _, ok := markVisible(page, emailSelector); ok && !signedIn {
			if err := signIn(ctx, page, account, code); err != nil {
				return err
			}
			signedIn = true
			deadline = time.Now().Add(answerWait)
		}
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
	}
	if _, ok := markVisible(page, emailSelector); ok {
		return errSignInNeeded
	}
	return errors.New("united: no search answer captured")
}

// missedJS reports whether the page has called the search API, whose
// answer may then have come before it was being watched.
const missedJS = `performance.getEntriesByType('resource')` +
	`.some(e => e.name.includes('/api/flight/FetchFlights'))`

// missed reports whether page called the search API.
func missed(page *browser.Page) bool {
	v, err := page.Evaluate(missedJS)
	return err == nil && v == true
}

// signIn fills in the sign-in dialog on page with account's email or
// MileagePlus number and password, ticks remember me, and follows the
// dialog until it closes. If the site asks for a verification code, the
// code comes from code; with no code it returns errSignInNeeded. It
// returns errSignInNeeded when no account is configured, an error with
// the site's message when the sign-in is refused, and an error when a
// field never appears or the dialog never closes.
func signIn(
	ctx context.Context,
	page *browser.Page,
	account Account,
	code CodeFunc,
) error {
	if account.Email == "" || account.Password == "" {
		return errSignInNeeded
	}
	email, err := waitVisible(ctx, page, emailSelector)
	if err != nil {
		return err
	}
	if err := typeInto(page, email, account.Email); err != nil {
		return err
	}
	if err := clickButton(page, "Continue"); err != nil {
		return err
	}
	password, err := waitVisible(ctx, page, passwordSelector)
	if err != nil {
		if msg := visibleError(page); msg != "" {
			return refused(msg)
		}
		return err
	}
	if err := typeInto(page, password, account.Password); err != nil {
		return err
	}
	if box, ok := markVisible(page, rememberSelector); ok {
		_ = tick(page, box)
	}
	if err := clickButton(page, "Sign in"); err != nil {
		return err
	}
	return followSubmit(ctx, page, code)
}

// followSubmit waits for the sign-in dialog on page to close after the
// password went in, answering a verification code with code if the site
// asks for one. It returns errSignInNeeded when a code is asked for and
// code is nil, an error with the site's message when the sign-in is
// refused, and an error when the dialog does not close in time.
func followSubmit(
	ctx context.Context,
	page *browser.Page,
	code CodeFunc,
) error {
	coded := false
	deadline := time.Now().Add(submitWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, 2*pollDelay); err != nil {
			return err
		}
		if !dialogShown(page) {
			return nil
		}
		field, ok := codeStep(page)
		if msg := visibleError(page); msg != "" && !isPrompt(msg) {
			return refused(msg)
		}
		if coded || !ok {
			continue
		}
		if code == nil {
			return errSignInNeeded
		}
		if err := answerCode(ctx, page, field, code); err != nil {
			return err
		}
		coded = true
		deadline = time.Now().Add(submitWait)
	}
	return errors.New("united: sign-in did not finish")
}

// codeStep tags the field of the verification step page shows, which
// asks for a code the site sent the member, and returns a selector for
// it. It returns false when page shows no such step.
func codeStep(page *browser.Page) (string, bool) {
	if field, ok := markVisible(page, codeSelector); ok {
		return field, true
	}
	if !isPrompt(dialogText(page)) {
		return "", false
	}
	return markVisible(page, anyFieldSelector)
}

// isPrompt reports whether msg asks for the verification code rather
// than saying something went wrong.
func isPrompt(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "enter the verification")
}

// dialogText is the text the sign-in dialog on page shows, "" when none.
func dialogText(page *browser.Page) string {
	v, err := page.Evaluate(`[...document.querySelectorAll('[role=dialog]')]` +
		`.filter(e => e.offsetParent).map(e => e.innerText).join(' ')`)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// answerCode asks code for the verification code, types it into field on
// page and submits it with its button, or with Enter when it has none.
// It returns an error when code fails or the field cannot be filled in or
// submitted.
func answerCode(
	ctx context.Context,
	page *browser.Page,
	field string,
	code CodeFunc,
) error {
	c, err := code(ctx)
	if err != nil {
		return fmt.Errorf("united: verification code: %w", err)
	}
	if err := typeInto(page, field, strings.TrimSpace(c)); err != nil {
		return err
	}
	if box, ok := markVisible(page, rememberSelector); ok {
		_ = tick(page, box)
	}
	for _, label := range []string{"Continue", "Submit", "Verify"} {
		if clickButton(page, label) == nil {
			return nil
		}
	}
	if err := page.Press(field, "Enter"); err != nil {
		return fmt.Errorf("united: submit code: %w", err)
	}
	return nil
}

// refused builds the error for a sign-in the site refused with msg,
// keeping it short.
func refused(msg string) error {
	if len(msg) > 90 {
		msg = msg[:90]
	}
	return fmt.Errorf("united: sign-in refused: %s", msg)
}

// dialogShown reports whether page still shows the sign-in dialog.
func dialogShown(page *browser.Page) bool {
	_, ok := markVisible(page, `[role=dialog] input`)
	return ok
}

// tick ticks the checkbox sel on page unless it is ticked already. It
// returns an error when it cannot be clicked.
func tick(page *browser.Page, sel string) error {
	s, _ := json.Marshal(sel)
	v, err := page.Evaluate(`document.querySelector(` + string(s) +
		`).checked`)
	if err == nil && v == true {
		return nil
	}
	return page.Click(sel)
}

// visibleError returns the text of the error message the sign-in dialog
// shows, or "" when it shows none.
func visibleError(page *browser.Page) string {
	v, err := page.Evaluate(`[...document.querySelectorAll(` +
		`'[role=dialog] [role=alert], [role=dialog] [class*=rror]')]` +
		`.filter(e => e.offsetParent && e.innerText.trim())` +
		`.map(e => e.innerText.trim())[0] || ''`)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return strings.Join(strings.Fields(s), " ")
}

// marks numbers the elements mark tags.
var marks atomic.Int64

// markVisible tags the first shown element on page matching selector and
// returns a selector for just that element. It returns false when no
// shown element matches.
func markVisible(page *browser.Page, selector string) (string, bool) {
	sel, _ := json.Marshal(selector)
	return mark(page, `[...document.querySelectorAll(`+string(sel)+`)]`+
		`.find(x => x.offsetParent !== null)`)
}

// clickButton clicks the shown button in the sign-in dialog on page whose
// text is label. It returns an error when there is none or the click
// fails.
func clickButton(page *browser.Page, label string) error {
	l, _ := json.Marshal(label)
	sel, ok := mark(page, `[...document.querySelectorAll(`+
		`'[role=dialog] button')].find(x => x.offsetParent !== null && `+
		`x.innerText.trim() === `+string(l)+`)`)
	if !ok {
		return fmt.Errorf("united: sign-in dialog has no %s button", label)
	}
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("united: click %s: %w", label, err)
	}
	return nil
}

// mark evaluates find, a JavaScript expression giving an element or
// nothing, on page, tags the element it gives and returns a selector for
// it. It returns false when find gives nothing or fails.
func mark(page *browser.Page, find string) (string, bool) {
	id := strconv.FormatInt(marks.Add(1), 10)
	v, err := page.Evaluate(`(() => { const e = ` + find + `;` +
		`if (!e) return false;` +
		`e.setAttribute('data-fc-mark', '` + id + `');` +
		`return true; })()`)
	if err != nil || v != true {
		return "", false
	}
	return `[data-fc-mark="` + id + `"]`, true
}

// waitVisible waits up to formWait for a shown element matching selector
// on page and returns a selector for it. It returns an error when none
// appears in time or ctx ends.
func waitVisible(
	ctx context.Context,
	page *browser.Page,
	selector string,
) (string, error) {
	deadline := time.Now().Add(formWait)
	for time.Now().Before(deadline) {
		if sel, ok := markVisible(page, selector); ok {
			return sel, nil
		}
		if err := sleep(ctx, pollDelay/2); err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("united: sign-in form has no %s", selector)
}

// typeInto clicks the element sel on page and types text into it key by
// key. It returns an error when the element cannot be clicked or typed
// into.
func typeInto(page *browser.Page, sel, text string) error {
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("united: sign-in form: %w", err)
	}
	if err := page.Type(sel, text); err != nil {
		return fmt.Errorf("united: sign-in form: %w", err)
	}
	return nil
}

// sleep waits d, returning ctx's error if it ends first.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
