package aeroplan

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

// errSignInNeeded is returned when aircanada.com asks for a sign-in that
// cannot be done without a person: no account is configured, or the site
// wants the code it emails to the member because the saved session no
// longer marks the browser as a trusted device. Running the aeroplan-login
// command again fixes it.
var errSignInNeeded = errors.New("aeroplan: sign-in needed")

// Account is the Aeroplan member Search signs in as.
type Account struct {
	// Email is the member's sign-in email or Aeroplan number.
	Email string
	// Password is the member's password.
	Password string
	// SessionFile is where the signed-in session's cookies are kept;
	// empty keeps them under the user cache directory.
	SessionFile string
}

// AccountFromEnv reads the account from AEROPLAN_EMAIL, AEROPLAN_PASSWORD
// and AEROPLAN_SESSION_FILE. It returns false when the email or the
// password is unset, in which case the program should not be offered.
func AccountFromEnv() (Account, bool) {
	a := Account{
		Email:       os.Getenv("AEROPLAN_EMAIL"),
		Password:    os.Getenv("AEROPLAN_PASSWORD"),
		SessionFile: os.Getenv("AEROPLAN_SESSION_FILE"),
	}
	return a, a.Email != "" && a.Password != ""
}

// sessionPath is where the account's session is kept: SessionFile, or
// flight-club/aeroplan-session.json under the user cache directory. It is
// empty when neither is known, which keeps no session.
func (a Account) sessionPath() string {
	if a.SessionFile != "" {
		return a.SessionFile
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "flight-club", "aeroplan-session.json")
}

// session is the signed-in session's cookies, kept in a JSON file so it
// outlives the process. It also remembers that the site wanted a person
// to sign in, so searches do not keep signing in, each of which makes the
// site send the member another code, until the file changes. It is safe
// for concurrent use.
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
// has not changed since, such as by the aeroplan-login command.
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
		return fmt.Errorf("aeroplan: session file: %w", err)
	}
	tmp := s.path + ".new"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("aeroplan: session file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("aeroplan: session file: %w", err)
	}
	return nil
}

// CodeFunc returns the verification code aircanada.com emailed to the
// member, such as by asking the person at the terminal for it.
type CodeFunc func(ctx context.Context) (string, error)

// SignIn signs the member in on aircanada.com, answering the emailed
// verification code with code if the site asks for one and asking it to
// remember the browser, then saves the session to the account's session
// file, so later searches sign in, or stay signed in, without a code. It
// returns an error when the sign-in is refused or does not finish, or the
// session cannot be saved; it returns the path the session was saved to.
func (p *Program) SignIn(ctx context.Context, code CodeFunc) (string, error) {
	q := award.Query{
		Origin:      "YYZ",
		Destination: "YVR",
		Date:        time.Now().AddDate(0, 0, 30),
		Passengers:  1,
	}
	captured, err := p.browser.Collect(ctx, bookingURL(q), browser.CollectOpts{
		Timeout:     loadTimeout,
		WarmUp:      homeURL,
		Cookies:     p.session.load(),
		ScrollDelay: settleDelay,
		Scrolls:     1,
		Accept:      func(string, string) bool { return false },
		Interact: func(ctx context.Context, page *browser.Page) error {
			return signInIfAsked(ctx, page, p.account, code)
		},
	})
	if err != nil {
		return "", err
	}
	if strings.Contains(captured.URL, loginPath) {
		return "", errSignInNeeded
	}
	if err := p.session.save(captured.Cookies); err != nil {
		return "", err
	}
	return p.session.path, nil
}

// Timings of the sign-in: how long the results page is watched for the
// redirect to the sign-in page or the start of the search, how long the
// sign-in form is given to appear, how long the bot protection's sensor
// watches the pointer move before the form is filled in, and how long a
// submitted form is given to lead anywhere.
const (
	redirectWait = 25 * time.Second
	formWait     = 30 * time.Second
	sensorWait   = 12 * time.Second
	submitWait   = 90 * time.Second
	pollDelay    = 2 * time.Second
)

// searchStartedJS reports whether the page has called the availability
// API, which it only does for a signed-in member.
const searchStartedJS = `performance.getEntriesByType('resource')` +
	`.some(e => e.name.includes('air-bounds'))`

// signInIfAsked watches page, freshly opened on the results page, until
// either it starts the search, which needs nothing, or it is sent to the
// sign-in page, where it signs account in with signIn. It returns what
// signIn returns, or nil when the page did neither in time.
func signInIfAsked(
	ctx context.Context,
	page *browser.Page,
	account Account,
	code CodeFunc,
) error {
	deadline := time.Now().Add(redirectWait)
	for time.Now().Before(deadline) {
		if onLoginPage(page) {
			return signIn(ctx, page, account, code)
		}
		if started, _ := page.Evaluate(searchStartedJS); started == true {
			return nil
		}
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
	}
	return nil
}

// signIn fills in the sign-in form on page with account's email and
// password, moving the pointer about first so the bot protection lets the
// form through, and follows the site until it leaves the sign-in page. If
// the site asks for the code it emailed, the code comes from code and the
// browser is asked to be remembered; with no code it returns
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
	if account.Email == "" || account.Password == "" {
		return errSignInNeeded
	}
	email, err := waitVisible(ctx, page, `input[id^="gigya-loginID"]`)
	if err != nil {
		return err
	}
	if err := wander(ctx, page, sensorWait); err != nil {
		return err
	}
	password, err := waitVisible(ctx, page, `input[id^="gigya-password"]`)
	if err != nil {
		return err
	}
	if err := typeInto(page, email, account.Email); err != nil {
		return err
	}
	if err := typeInto(page, password, account.Password); err != nil {
		return err
	}
	if err := clickVisible(ctx, page, `input[type="submit"]`); err != nil {
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
			return fmt.Errorf("aeroplan: sign-in refused: %s", msg)
		}
		if coded {
			continue
		}
		if !tfaShown(page) {
			continue
		}
		if code == nil {
			return errSignInNeeded
		}
		if err := answerCode(ctx, page, code); err != nil {
			return err
		}
		coded = true
		deadline = time.Now().Add(submitWait)
	}
	return errors.New("aeroplan: sign-in did not finish")
}

// codeSelector matches the fields of the verification step that take a
// code, whichever way the code was sent.
const codeSelector = `input[name="code"], input[name*="Code_"]`

// emailCodeSelector matches the fields that take a code sent by email
// rather than by text message.
const emailCodeSelector = `input[name="code"], ` +
	`input[name*="Code_"]:not([name^="phoneCode"])`

// tfaShown reports whether page shows the verification step, which asks
// for a code the site sent the member.
func tfaShown(page *browser.Page) bool {
	_, ok := markVisible(page, codeSelector)
	return ok
}

// answerCode has the site email the verification code, asking for it by
// email when the step offers a text message first, asks code for it,
// types it in, ticks the visible remember-this-device box if there is
// one, and submits the step. It returns an error when no email code field
// appears, code fails or the step cannot be filled in or submitted.
func answerCode(
	ctx context.Context,
	page *browser.Page,
	code CodeFunc,
) error {
	field, ok := markVisible(page, emailCodeSelector)
	if !ok {
		if link, found := markText(page, "Send Code"); found {
			if err := page.Click(link); err != nil {
				return fmt.Errorf("aeroplan: send email code: %w", err)
			}
		}
		var err error
		field, err = waitVisible(ctx, page, emailCodeSelector)
		if err != nil {
			return err
		}
	}
	c, err := code(ctx)
	if err != nil {
		return fmt.Errorf("aeroplan: verification code: %w", err)
	}
	if err := typeInto(page, field, strings.TrimSpace(c)); err != nil {
		return err
	}
	if box, ok := markVisible(page, rememberSelector); ok {
		_ = page.Click(box)
	}
	button, ok := markButtonNear(page, field)
	if !ok {
		return errors.New("aeroplan: verification step has no button")
	}
	if err := page.Click(button); err != nil {
		return fmt.Errorf("aeroplan: submit code: %w", err)
	}
	return nil
}

// rememberSelector matches the box that asks the site to remember the
// browser, so the next sign-in needs no emailed code.
const rememberSelector = `input[type="checkbox"][name*="emember"],` +
	`input[type="checkbox"][name*="rust"]`

// onLoginPage reports whether page is on the site's sign-in pages.
func onLoginPage(page *browser.Page) bool {
	u, err := page.URL()
	return err == nil && strings.Contains(u, loginPath)
}

// visibleError returns the text of the error message the sign-in form
// shows, or "" when it shows none.
func visibleError(page *browser.Page) string {
	v, err := page.Evaluate(`[...document.querySelectorAll(` +
		`'.gigya-error-msg-active, .gigya-form-error-msg-active')]` +
		`.filter(e => e.offsetParent && e.innerText.trim())` +
		`.map(e => e.innerText.trim()).join(' ')`)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// marks numbers the elements markVisible tags.
var marks atomic.Int64

// returns a selector for just that element. It returns false when no
// shown element matches.
func markVisible(page *browser.Page, selector string) (string, bool) {
	sel, _ := json.Marshal(selector)
	return mark(page, `[...document.querySelectorAll(`+string(sel)+`)]`+
		`.find(x => x.offsetParent !== null)`)
}

// markText tags the innermost shown element on page whose text is text,
// such as a link, and returns a selector for it. It returns false when
// there is none.
func markText(page *browser.Page, text string) (string, bool) {
	t, _ := json.Marshal(text)
	return mark(page, `[...document.querySelectorAll('a, button, span, div')]`+
		`.filter(x => x.offsetParent !== null && `+
		`x.innerText.trim() === `+string(t)+`)`+
		`.find(x => ![...x.children].some(c => `+
		`c.innerText.trim() === `+string(t)+`))`)
}

// markButtonNear tags the shown submit button closest to the element
// field selects, the one that submits the step field belongs to rather
// than a link styled as a button such as Resend, and returns a selector
// for it. It returns false when there is none.
func markButtonNear(page *browser.Page, field string) (string, bool) {
	f, _ := json.Marshal(field)
	return mark(page, `(() => {`+
		`const shown = n => [...n.querySelectorAll('input[type=submit],`+
		` input[type=button]')].find(x => x.offsetParent !== null);`+
		`for (let n = document.querySelector(`+string(f)+`); n;`+
		` n = n.parentElement) { const b = shown(n); if (b) return b; }`+
		`return null; })()`)
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
	return "", fmt.Errorf("aeroplan: sign-in form has no %s", selector)
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
		return fmt.Errorf("aeroplan: click %s: %w", selector, err)
	}
	return nil
}

// typeInto clicks the element sel on page and types text into it key by
// key. It returns an error when the element cannot be clicked or typed
// into.
func typeInto(page *browser.Page, sel, text string) error {
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("aeroplan: sign-in form: %w", err)
	}
	if err := page.Type(sel, text); err != nil {
		return fmt.Errorf("aeroplan: sign-in form: %w", err)
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
