package qatar

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

// errSignInNeeded is returned when qatarairways.com asks for a sign-in
// that cannot be done without a person: no account is configured, or the
// site wants the one-time pin it emails to the member. Signing in again
// with SignIn and a CodeFunc fixes it.
var errSignInNeeded = errors.New("qatar: sign-in needed")

// Account is the Privilege Club member Search signs in as.
type Account struct {
	// Email is the member's sign-in email or membership number.
	Email string
	// Password is the member's password.
	Password string
	// Member is the membership number; when set it is used to sign in
	// instead of Email.
	Member string
	// SessionFile is where the signed-in session's cookies are kept;
	// empty keeps them under the user cache directory.
	SessionFile string
}

// AccountFromEnv reads the account from QATAR_EMAIL, QATAR_PASSWORD,
// QATAR_MEMBER and QATAR_SESSION_FILE. It returns false when the email or
// the password is unset, in which case the program should not be offered.
func AccountFromEnv() (Account, bool) {
	a := Account{
		Email:       os.Getenv("QATAR_EMAIL"),
		Password:    os.Getenv("QATAR_PASSWORD"),
		Member:      os.Getenv("QATAR_MEMBER"),
		SessionFile: os.Getenv("QATAR_SESSION_FILE"),
	}
	return a, a.Email != "" && a.Password != ""
}

// username is what the sign-in form is given as the member: the
// membership number when known, else the email.
func (a Account) username() string {
	if a.Member != "" {
		return a.Member
	}
	return a.Email
}

// sessionPath is where the account's session is kept: SessionFile, or
// flight-club/qatar-session.json under the user cache directory. It is
// empty when neither is known, which keeps no session.
func (a Account) sessionPath() string {
	if a.SessionFile != "" {
		return a.SessionFile
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "flight-club", "qatar-session.json")
}

// session is the signed-in session's cookies, kept in a JSON file so it
// outlives the process. It also remembers that the site wanted a person
// to sign in, so searches do not keep signing in, each of which emails
// the member another pin or counts towards locking the account, until
// the file changes. It is safe for concurrent use.
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
// has not changed since, such as by a sign-in with SignIn.
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
		return fmt.Errorf("qatar: session file: %w", err)
	}
	tmp := s.path + ".new"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("qatar: session file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("qatar: session file: %w", err)
	}
	return nil
}

// CodeFunc returns the one-time pin qatarairways.com emailed to the
// member, such as by asking the person at the terminal for it.
type CodeFunc func(ctx context.Context) (string, error)

// SignIn signs the member in on qatarairways.com, answering the emailed
// one-time pin with code when the site asks for one, then opens an award
// search to check the session works and saves it to the account's
// session file, so later searches stay signed in without a pin. It
// returns an error when the sign-in is refused or does not finish, or the
// session cannot be saved; it returns the path the session was saved to.
func (p *Program) SignIn(ctx context.Context, code CodeFunc) (string, error) {
	q := award.Query{
		Origin:      "DOH",
		Destination: "LHR",
		Date:        time.Now().AddDate(0, 0, 30),
		Passengers:  1,
	}
	booking := bookingURL(q)
	signedIn := false
	captured, err := p.browser.Collect(ctx, loginURL, browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Cookies:     p.session.load(),
		ScrollDelay: settleDelay,
		Scrolls:     1,
		Accept:      func(string, string) bool { return false },
		Interact: func(ctx context.Context, page *browser.Page) error {
			if onLoginPage(page) {
				err := signIn(ctx, page, p.account, code)
				if err != nil {
					return err
				}
			}
			if err := page.Navigate(booking); err != nil {
				return fmt.Errorf("qatar: open search: %w", err)
			}
			state, err := waitResults(ctx, page)
			signedIn = state == stateResults
			return err
		},
	})
	if err != nil {
		return "", err
	}
	if !signedIn {
		return "", errSignInNeeded
	}
	if err := p.session.save(captured.Cookies); err != nil {
		return "", err
	}
	return p.session.path, nil
}

// Timings of the sign-in: how long a page is watched for the search or
// the sign-in prompt, how long the sign-in form is given to appear, how
// long the pointer moves about before the form is filled in, and how long
// a submitted form is given to lead anywhere.
const (
	resultsWait = 45 * time.Second
	formWait    = 30 * time.Second
	sensorWait  = 6 * time.Second
	submitWait  = 90 * time.Second
	pollDelay   = 2 * time.Second
)

// pageState is what a results page turned out to show.
type pageState int

// The states of a results page.
const (
	stateUnknown pageState = iota
	stateResults
	stateLoggedOut
)

// searchStartedJS reports whether the page has called the award search
// API, which it only does for a signed-in member.
const searchStartedJS = `performance.getEntriesByType('resource')` +
	`.some(e => e.name.includes('` + offersPath + `'))`

// loggedOutJS reports whether the results page asks the visitor to log
// in, which it does when no member is signed in.
const loggedOutJS = `[...document.querySelectorAll('button#login')]` +
	`.some(e => e.offsetParent !== null)`

// waitResults watches page, freshly opened on the results page, until it
// either calls the award search API or asks to log in, and says which. It
// returns stateUnknown when it did neither in time, and an error only
// when ctx ends.
func waitResults(ctx context.Context, page *browser.Page) (pageState, error) {
	deadline := time.Now().Add(resultsWait)
	for time.Now().Before(deadline) {
		if started, _ := page.Evaluate(searchStartedJS); started == true {
			return stateResults, nil
		}
		if out, _ := page.Evaluate(loggedOutJS); out == true {
			return stateLoggedOut, nil
		}
		if err := sleep(ctx, pollDelay); err != nil {
			return stateUnknown, err
		}
	}
	return stateUnknown, nil
}

// signInIfAsked watches page, freshly opened on the results page, and
// when it asks to log in, signs account in on the sign-in page with
// signIn and opens booking again. It returns what signIn returns, an
// error when booking cannot be opened again, or nil when no sign-in was
// asked for.
func signInIfAsked(
	ctx context.Context,
	page *browser.Page,
	account Account,
	booking string,
	code CodeFunc,
) error {
	state, err := waitResults(ctx, page)
	if err != nil || state != stateLoggedOut {
		return err
	}
	if err := page.Navigate(loginURL); err != nil {
		return fmt.Errorf("qatar: open sign-in page: %w", err)
	}
	if err := signIn(ctx, page, account, code); err != nil {
		return err
	}
	if err := page.Navigate(booking); err != nil {
		return fmt.Errorf("qatar: open search: %w", err)
	}
	state, err = waitResults(ctx, page)
	if err != nil {
		return err
	}
	if state == stateLoggedOut {
		return errSignInNeeded
	}
	return nil
}

// Selectors of the sign-in page's form and its one-time pin step.
const (
	usernameSelector = `#f1003`
	passwordSelector = `#f1001`
	submitSelector   = `#loginButtonInvoke`
	otpSelector      = `#otp-value`
	verifySelector   = `#otp-verify-button`
)

// signIn fills in the sign-in form on page, which must be on the sign-in
// page, with account's username and password, and follows the site until
// it leaves the sign-in page. If the site asks for the one-time pin it
// emailed, the pin comes from code; with no code it returns
// errSignInNeeded. It returns errSignInNeeded when no account is
// configured, an error with the site's message when the sign-in is
// refused (such as a wrong password), and an error when the form never
// appears or the site never leaves the sign-in page.
func signIn(
	ctx context.Context,
	page *browser.Page,
	account Account,
	code CodeFunc,
) error {
	if account.username() == "" || account.Password == "" {
		return errSignInNeeded
	}
	user, err := waitVisible(ctx, page, usernameSelector)
	if err != nil {
		return err
	}
	if err := wander(ctx, page, sensorWait); err != nil {
		return err
	}
	if err := typeInto(page, user, account.username()); err != nil {
		return err
	}
	password, err := waitVisible(ctx, page, passwordSelector)
	if err != nil {
		return err
	}
	if err := typeInto(page, password, account.Password); err != nil {
		return err
	}
	if err := clickVisible(ctx, page, submitSelector); err != nil {
		return err
	}
	coded := false
	deadline := time.Now().Add(submitWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
		if !onLoginPage(page) {
			return nil
		}
		if msg := visibleError(page); msg != "" {
			return fmt.Errorf("qatar: sign-in refused: %s", clip(msg, 80))
		}
		if coded {
			continue
		}
		field, ok := markVisible(page, otpSelector)
		if !ok {
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
	return errors.New("qatar: sign-in did not finish")
}

// answerCode asks code for the one-time pin, types it into the field
// selected by field on page and submits it. It returns an error when code
// fails or the step cannot be filled in or submitted.
func answerCode(
	ctx context.Context,
	page *browser.Page,
	field string,
	code CodeFunc,
) error {
	c, err := code(ctx)
	if err != nil {
		return fmt.Errorf("qatar: one-time pin: %w", err)
	}
	if err := typeInto(page, field, strings.TrimSpace(c)); err != nil {
		return err
	}
	return clickVisible(ctx, page, verifySelector)
}

// onLoginPage reports whether page is on the site's sign-in page.
func onLoginPage(page *browser.Page) bool {
	u, err := page.URL()
	return err == nil && strings.Contains(u, loginPath)
}

// visibleError returns the text of the error message the sign-in page
// shows, or "" when it shows none.
func visibleError(page *browser.Page) string {
	v, err := page.Evaluate(`[...document.querySelectorAll(` +
		`'#loginErrorBlock .input-base-msg-box, #login-alert-message,` +
		` .otp-error, #otp-error')]` +
		`.filter(e => e.offsetParent && e.innerText.trim())` +
		`.map(e => e.innerText.trim()).join(' ')`)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// clip shortens s to at most n bytes.
func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// marks numbers the elements markVisible tags.
var marks atomic.Int64

// markVisible tags the first shown element on page matching selector and
// returns a selector for just that element. It returns false when no
// shown element matches.
func markVisible(page *browser.Page, selector string) (string, bool) {
	sel, _ := json.Marshal(selector)
	id := strconv.FormatInt(marks.Add(1), 10)
	v, err := page.Evaluate(`(() => { const e = [...document` +
		`.querySelectorAll(` + string(sel) + `)]` +
		`.find(x => x.offsetParent !== null);` +
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
	return "", fmt.Errorf("qatar: sign-in page has no %s", selector)
}

// clickVisible clicks the shown element matching selector on page,
// waiting for it like waitVisible. It returns an error when there is none
// or the click fails.
func clickVisible(
	ctx context.Context,
	page *browser.Page,
	selector string,
) error {
	sel, err := waitVisible(ctx, page, selector)
	if err != nil {
		return err
	}
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("qatar: click %s: %w", selector, err)
	}
	return nil
}

// typeInto clicks the element sel on page and types text into it key by
// key. It returns an error when the element cannot be clicked or typed
// into.
func typeInto(page *browser.Page, sel, text string) error {
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("qatar: sign-in form: %w", err)
	}
	if err := page.Type(sel, text); err != nil {
		return fmt.Errorf("qatar: sign-in form: %w", err)
	}
	return nil
}

// wander moves the pointer about page for d, the way a person reading
// the form would. It returns an error only when ctx ends.
func wander(ctx context.Context, page *browser.Page, d time.Duration) error {
	const steps = 12
	for i := range steps {
		_ = page.Mouse().Move(
			float64(300+i*47%700),
			float64(200+i*31%500),
		)
		if err := sleep(ctx, d/steps); err != nil {
			return err
		}
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
