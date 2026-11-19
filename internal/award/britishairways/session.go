package britishairways

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joakimcarlsson/flight-club/internal/browser"
)

// errSignInNeeded is returned when britishairways.com asks for a sign-in
// that cannot be done without a person: no account is configured, the
// site wants a code it sent the member, or its captcha asks for a puzzle.
// Running the sign-in command again fixes it.
var errSignInNeeded = errors.New("britishairways: sign-in needed")

// Account is the British Airways Club member Search signs in as.
type Account struct {
	// Email is the member's sign-in email, username or membership number.
	Email string
	// Password is the member's password.
	Password string
	// SessionFile is where the signed-in session's cookies are kept;
	// empty keeps them under the user cache directory.
	SessionFile string
}

// AccountFromEnv reads the account from BA_EMAIL, BA_PASSWORD and
// BA_SESSION_FILE. It returns false when the email or the password is
// unset, in which case the program should not be offered.
func AccountFromEnv() (Account, bool) {
	a := Account{
		Email:       os.Getenv("BA_EMAIL"),
		Password:    os.Getenv("BA_PASSWORD"),
		SessionFile: os.Getenv("BA_SESSION_FILE"),
	}
	return a, a.Email != "" && a.Password != ""
}

// sessionPath is where the account's session is kept: SessionFile, or
// flight-club/britishairways-session.json under the user cache directory.
// It is empty when neither is known, which keeps no session.
func (a Account) sessionPath() string {
	if a.SessionFile != "" {
		return a.SessionFile
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "flight-club", "britishairways-session.json")
}

// session is the signed-in session's cookies, kept in a JSON file so it
// outlives the process. It also remembers that the site wanted a person
// to sign in, so searches do not keep trying, until the file changes. It
// is safe for concurrent use.
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
// has not changed since, such as by the sign-in command.
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
		return fmt.Errorf("britishairways: session file: %w", err)
	}
	tmp := s.path + ".new"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("britishairways: session file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("britishairways: session file: %w", err)
	}
	return nil
}

// CodeFunc returns the verification code britishairways.com sent the
// member, such as by asking the person at the terminal for it.
type CodeFunc func(ctx context.Context) (string, error)

// SignIn signs the member in on britishairways.com, answering a
// verification code with code if the site asks for one, then saves the
// session to the account's session file so later searches stay signed in.
// It returns the path the session was saved to, and an error when the
// sign-in is refused or does not finish, or the session cannot be saved.
func (p *Program) SignIn(ctx context.Context, code CodeFunc) (string, error) {
	captured, err := p.browser.Collect(ctx, formURL, browser.CollectOpts{
		Timeout: loadTimeout,
		Cookies: p.session.load(),
		Accept:  func(string, string) bool { return false },
		Interact: func(ctx context.Context, page *browser.Page) error {
			if signedIn(page) {
				return nil
			}
			return signIn(ctx, page, p.account, code)
		},
	})
	if err != nil {
		return "", err
	}
	if err := p.session.save(captured.Cookies); err != nil {
		return "", err
	}
	return p.session.path, nil
}

// Timings of the sign-in: how long the sign-in form is given to appear,
// how long the pointer wanders before the form is filled in, how long the
// captcha is given to pass and how long a submitted form is given to
// lead anywhere.
const (
	formWait    = 40 * time.Second
	sensorWait  = 4 * time.Second
	captchaWait = 20 * time.Second
	submitWait  = 90 * time.Second
	pollDelay   = 2 * time.Second
)

// preLoginURL is the page that offers the sign-in, sending the member to
// the results form afterwards.
var preLoginURL = "https://www.britishairways.com/nx/b/account/en/gbr/" +
	"account/pre-login/?returnTo=" + url.QueryEscape(formURL)

// loginHost is the host of the sign-in pages.
const loginHost = "accounts.britishairways.com"

// signedIn reports whether page shows a signed-in member, which the site
// marks with a Log out link.
func signedIn(page *browser.Page) bool {
	v, err := page.Evaluate(`/\bLog out\b/.test(document.body.innerText)`)
	return err == nil && v == true
}

// signIn signs account in from page: it opens the sign-in page, fills in
// the email and password, ticks the captcha, submits, answers a code with
// code if one is asked for, and comes back to the results form. It
// returns errSignInNeeded when no account is configured, a code is needed
// without code, or the captcha wants a puzzle; an error with the site's
// message when the sign-in is refused; and an error when the form never
// appears or the site never comes back.
func signIn(
	ctx context.Context,
	page *browser.Page,
	account Account,
	code CodeFunc,
) error {
	if account.Email == "" || account.Password == "" {
		return errSignInNeeded
	}
	if err := page.Navigate(preLoginURL); err != nil {
		return fmt.Errorf("britishairways: sign-in page: %w", err)
	}
	if err := openLoginForm(ctx, page); err != nil {
		return err
	}
	if !onLoginPage(page) && signedIn(page) {
		return backToForm(page)
	}
	if err := fillLoginForm(ctx, page, account); err != nil {
		return err
	}
	if err := submitLogin(ctx, page, code); err != nil {
		return err
	}
	if err := settle(ctx, page); err != nil {
		return err
	}
	return backToForm(page)
}

// settleWait bounds how long the site is given to finish a sign-in once
// it has left the sign-in pages.
const settleWait = 45 * time.Second

// settle waits for page to finish the sign-in the sign-in pages handed
// back, which the pre-login page does before sending the member on, so
// leaving it early would lose the session. It returns an error only when
// ctx ends.
func settle(ctx context.Context, page *browser.Page) error {
	deadline := time.Now().Add(settleWait)
	for time.Now().Before(deadline) {
		u, _ := page.URL()
		if !strings.Contains(u, "pre-login") && !onLoginPage(page) &&
			signedIn(page) {
			return nil
		}
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
	}
	return nil
}

// openLoginForm accepts the cookie banner on the pre-login page and
// presses its Log in button, then waits for the sign-in form. It returns
// at once when the site signs the member in without a form, and an error
// when the button or the form never appears.
func openLoginForm(ctx context.Context, page *browser.Page) error {
	deadline := time.Now().Add(formWait)
	for time.Now().Before(deadline) {
		acceptCookies(page)
		if onLoginPage(page) {
			_, err := waitVisible(ctx, page, "#username")
			return err
		}
		if btn, ok := mark(page, loginButtonJS); ok {
			_ = page.Click(btn)
		}
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
		if !onLoginPage(page) && signedIn(page) {
			return nil
		}
	}
	return errors.New("britishairways: sign-in form did not appear")
}

// loginButtonJS finds the pre-login page's own Log in button, not the one
// in its header.
const loginButtonJS = `[...document.querySelectorAll('button')]` +
	`.find(b => b.offsetParent && b.innerText.trim() === 'Log in'` +
	` && !b.closest('header'))`

// acceptCookies accepts the cookie banner on page if it shows one.
func acceptCookies(page *browser.Page) {
	_, _ = page.Evaluate(`(() => { const b = document.querySelector(` +
		`'#onetrust-accept-btn-handler'); if (b && b.offsetParent) ` +
		`b.click(); })()`)
}

// fillLoginForm moves the pointer about, types account's email and
// password into the sign-in form on page and ticks the captcha if it
// shows one. It returns an error when a field cannot be typed into, and
// errSignInNeeded when the captcha wants a puzzle.
func fillLoginForm(
	ctx context.Context,
	page *browser.Page,
	account Account,
) error {
	if err := wander(ctx, page, sensorWait); err != nil {
		return err
	}
	if err := typeInto(page, "#username", account.Email); err != nil {
		return err
	}
	password, err := waitVisible(ctx, page, "#password")
	if err != nil {
		return err
	}
	if err := typeInto(page, password, account.Password); err != nil {
		return err
	}
	return tickCaptcha(ctx, page)
}

// submitLogin submits the sign-in form on page and follows the site until
// it leaves the sign-in pages, ticking the captcha again when the site
// asks for it and answering a verification code with code. It returns
// errSignInNeeded when a code is needed without code or the captcha wants
// a puzzle, an error with the site's message when the sign-in is refused,
// and an error when the site never leaves the sign-in pages.
func submitLogin(
	ctx context.Context,
	page *browser.Page,
	code CodeFunc,
) error {
	if err := clickVisible(ctx, page, submitSelector); err != nil {
		return err
	}
	coded, retried := false, false
	deadline := time.Now().Add(submitWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
		if !onLoginPage(page) {
			return nil
		}
		if msg := visibleError(page); msg != "" && !captchaShown(page) {
			return fmt.Errorf("britishairways: sign-in refused: %s",
				shorten(msg, 80))
		}
		if captchaShown(page) && !captchaPassed(page) && !retried {
			retried = true
			if err := tickCaptcha(ctx, page); err != nil {
				return err
			}
			if err := clickVisible(ctx, page, submitSelector); err != nil {
				return err
			}
			continue
		}
		if coded {
			continue
		}
		field, ok := markVisible(page, codeSelector)
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
	return errors.New("britishairways: sign-in did not finish")
}

// submitSelector matches the sign-in pages' submit button.
const submitSelector = `button[type="submit"][name="action"]`

// codeSelector matches the field of a verification step that takes a code.
const codeSelector = `input#code, input[name="code"], input[name="otp"]`

// answerCode asks code for the verification code, types it into field on
// page and submits the step. It returns an error when code fails or the
// step cannot be filled in or submitted.
func answerCode(
	ctx context.Context,
	page *browser.Page,
	field string,
	code CodeFunc,
) error {
	c, err := code(ctx)
	if err != nil {
		return fmt.Errorf("britishairways: verification code: %w", err)
	}
	if err := typeInto(page, field, strings.TrimSpace(c)); err != nil {
		return err
	}
	return clickVisible(ctx, page, submitSelector)
}

// backToForm opens the results form on page again after a sign-in. It
// returns an error when the page cannot be opened.
func backToForm(page *browser.Page) error {
	if err := page.Navigate(formURL); err != nil {
		return fmt.Errorf("britishairways: results form: %w", err)
	}
	return nil
}

// captchaFrameJS gives the box of the shown hCaptcha checkbox frame as
// JSON, or null when there is none.
const captchaFrameJS = `JSON.stringify([...document.querySelectorAll(` +
	`'iframe[src*="hcaptcha"]')].map(f => f.getBoundingClientRect())` +
	`.find(r => r.width > 100 && r.height < 120 && r.top >= 0) || null)`

// captchaShown reports whether page shows the hCaptcha checkbox.
func captchaShown(page *browser.Page) bool {
	v, err := page.Evaluate(captchaFrameJS)
	s, _ := v.(string)
	return err == nil && s != "" && s != "null"
}

// captchaPassed reports whether the captcha on page has been solved.
func captchaPassed(page *browser.Page) bool {
	v, err := page.Evaluate(`[...document.querySelectorAll(` +
		`'[name="h-captcha-response"]')].some(e => e.value)`)
	return err == nil && v == true
}

// tickCaptcha clicks the hCaptcha checkbox on page, if it shows one that
// has not passed, and waits for it to pass. It returns errSignInNeeded
// when it does not pass in time, which is when it wants a puzzle solved.
func tickCaptcha(ctx context.Context, page *browser.Page) error {
	if !captchaShown(page) || captchaPassed(page) {
		return nil
	}
	v, err := page.Evaluate(captchaFrameJS)
	if err != nil {
		return fmt.Errorf("britishairways: captcha: %w", err)
	}
	s, _ := v.(string)
	var box struct {
		X, Y, Height float64
	}
	if json.Unmarshal([]byte(s), &box) != nil {
		return errors.New("britishairways: captcha not found")
	}
	x, y := box.X+30, box.Y+box.Height/2
	_ = page.Mouse().Move(x-40, y+20)
	if err := sleep(ctx, 300*time.Millisecond); err != nil {
		return err
	}
	if err := page.Mouse().Click(x, y); err != nil {
		return fmt.Errorf("britishairways: captcha: %w", err)
	}
	deadline := time.Now().Add(captchaWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, pollDelay/2); err != nil {
			return err
		}
		if captchaPassed(page) {
			return nil
		}
	}
	return errSignInNeeded
}

// onLoginPage reports whether page is on the sign-in pages.
func onLoginPage(page *browser.Page) bool {
	u, err := page.URL()
	return err == nil && strings.Contains(u, loginHost)
}

// visibleError returns the text of the error messages the sign-in form
// shows, or "" when it shows none.
func visibleError(page *browser.Page) string {
	v, err := page.Evaluate(`[...document.querySelectorAll(` +
		`'[id^="error-element"], .ulp-input-error-message, ` +
		`#prompt-alert')].filter(e => e.offsetParent && ` +
		`e.innerText.trim()).map(e => e.innerText.trim()).join(' ')`)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// shorten cuts s to at most n bytes.
func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n]
	}
	return s
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
	return "", fmt.Errorf("britishairways: page has no %s", selector)
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
		return fmt.Errorf("britishairways: click %s: %w", selector, err)
	}
	return nil
}

// typeInto clicks the element sel on page and types text into it key by
// key. It returns an error when the element cannot be clicked or typed
// into.
func typeInto(page *browser.Page, sel, text string) error {
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("britishairways: form: %w", err)
	}
	if err := page.Type(sel, text); err != nil {
		return fmt.Errorf("britishairways: form: %w", err)
	}
	return nil
}

// wander moves the pointer about page for d, the way a person reading
// the page would. It returns an error only when ctx ends.
func wander(ctx context.Context, page *browser.Page, d time.Duration) error {
	const steps = 10
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
