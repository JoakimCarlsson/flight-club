package emirates

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

// errSignInNeeded is returned when emirates.com asks for a sign-in that
// cannot be done without a person: no account is configured, or the site
// wants a code it sent the member. Running the sign-in again with a code
// fixes it.
var errSignInNeeded = errors.New("emirates: sign-in needed")

// errSignInRefused is returned when emirates.com refuses the account's
// email or Skywards number and password. Signing in again with the same
// details would only risk locking the account, so searches stop trying
// until the session file changes.
var errSignInRefused = errors.New("emirates: sign-in refused")

// Account is the Skywards member Search signs in as.
type Account struct {
	// Email is the member's sign-in email or Skywards number.
	Email string
	// Password is the member's password.
	Password string
	// SessionFile is where the signed-in session's cookies are kept;
	// empty keeps them under the user cache directory.
	SessionFile string
}

// AccountFromEnv reads the account from EMIRATES_EMAIL, EMIRATES_PASSWORD
// and EMIRATES_SESSION_FILE. It returns false when the email or the
// password is unset, in which case the program should not be offered.
func AccountFromEnv() (Account, bool) {
	a := Account{
		Email:       os.Getenv("EMIRATES_EMAIL"),
		Password:    os.Getenv("EMIRATES_PASSWORD"),
		SessionFile: os.Getenv("EMIRATES_SESSION_FILE"),
	}
	return a, a.Email != "" && a.Password != ""
}

// sessionPath is where the account's session is kept: SessionFile, or
// flight-club/emirates-session.json under the user cache directory. It is
// empty when neither is known, which keeps no session.
func (a Account) sessionPath() string {
	if a.SessionFile != "" {
		return a.SessionFile
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "flight-club", "emirates-session.json")
}

// session is the signed-in session's cookies, kept in a JSON file so it
// outlives the process. It also remembers that the site wanted a person
// to sign in, so searches do not keep signing in until the file changes.
// It is safe for concurrent use.
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
		return fmt.Errorf("emirates: session file: %w", err)
	}
	tmp := s.path + ".new"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("emirates: session file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("emirates: session file: %w", err)
	}
	return nil
}

// CodeFunc returns the verification code emirates.com sent the member,
// such as by asking the person at the terminal for it.
type CodeFunc func(ctx context.Context) (string, error)

// SignIn signs the member in on emirates.com, answering a verification
// code with code if the site asks for one and asking it to keep the
// member logged in, then saves the session to the account's session file,
// so later searches stay signed in without a code. It returns an error
// when the sign-in is refused or does not finish, or the session cannot
// be saved; it returns the path the session was saved to.
func (p *Program) SignIn(ctx context.Context, code CodeFunc) (string, error) {
	q := award.Query{
		Origin:      "DXB",
		Destination: "LHR",
		Date:        time.Now().AddDate(0, 0, 30),
		Passengers:  1,
	}
	captured, err := p.collect(ctx, bookingURL(q), code)
	if err != nil {
		return "", err
	}
	if signedOutURL(captured.URL) || onAccountsURL(captured.URL) {
		return "", errSignInNeeded
	}
	if err := p.session.save(captured.Cookies); err != nil {
		return "", err
	}
	return p.session.path, nil
}

// Timings of the sign-in: how long the results page is watched for the
// search's answer or the redirect away from it, how long the sign-in form
// is given to appear, how long the bot protection's sensor watches the
// pointer move before the form is filled in, and how long a submitted
// form is given to lead anywhere.
const (
	redirectWait = 45 * time.Second
	formWait     = 30 * time.Second
	sensorWait   = 6 * time.Second
	submitWait   = 90 * time.Second
	pollDelay    = 2 * time.Second
)

// accountsHost is the host of the sign-in pages.
const accountsHost = "accounts.emirates.com"

// signedOutMark is in the URL of the booking page the results page sends
// a visitor to when no member is signed in.
const signedOutMark = "error-code=SRP_"

// loginLinkSelector matches the booking page's link to the sign-in page.
const loginLinkSelector = `a[href*="accounts.emirates.com"]` +
	`[href*="/sso/"]`

// watch is what signInIfAsked needs besides the page: the member to sign
// in, how to get an emailed code, the results page to come back to and a
// report of whether the fares API has answered.
type watch struct {
	account  Account
	code     CodeFunc
	results  string
	answered func() bool
}

// signInIfAsked watches page, freshly opened on the results page, until
// either the fares API answers, which needs nothing, or it is sent away
// to sign in, where it signs w's account in with signIn and opens the
// results again. It returns what signIn returns, or nil when the page did
// neither in time.
func signInIfAsked(ctx context.Context, page *browser.Page, w watch) error {
	deadline := time.Now().Add(redirectWait)
	for time.Now().Before(deadline) {
		if w.answered() {
			return nil
		}
		u, _ := page.URL()
		if signedOutURL(u) || onAccountsURL(u) {
			if err := signIn(ctx, page, w.account, w.code); err != nil {
				return err
			}
			return reopen(ctx, page, w)
		}
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
	}
	return nil
}

// reopen opens w's results page on page again once the member is signed
// in and waits for the fares API to answer. It returns errSignInNeeded
// when the site still sends the page away, and an error when ctx ends.
func reopen(ctx context.Context, page *browser.Page, w watch) error {
	target, _ := json.Marshal(w.results)
	if _, err := page.Evaluate(
		"location.href = " + string(target),
	); err != nil {
		return fmt.Errorf("emirates: open results: %w", err)
	}
	arrived := false
	deadline := time.Now().Add(redirectWait)
	for time.Now().Before(deadline) {
		if err := sleep(ctx, pollDelay); err != nil {
			return err
		}
		if w.answered() {
			return nil
		}
		u, _ := page.URL()
		if strings.HasPrefix(u, resultsURL) {
			arrived = true
		}
		if arrived && (signedOutURL(u) || onAccountsURL(u)) {
			return errSignInNeeded
		}
	}
	return nil
}

// signIn follows page to the sign-in form, fills it in with account's
// email and password, moving the pointer about first so the bot
// protection lets the form through, and follows the site until it leaves
// the sign-in page. If the site asks for a code it sent, the code comes
// from code; with no code it returns errSignInNeeded. It returns
// errSignInNeeded when no account is configured, an error with the site's
// message when the sign-in is refused (such as a wrong password), and an
// error when the form never appears or the site never leaves it.
func signIn(
	ctx context.Context,
	page *browser.Page,
	account Account,
	code CodeFunc,
) error {
	if account.Email == "" || account.Password == "" {
		return errSignInNeeded
	}
	if err := openLogin(ctx, page); err != nil {
		return err
	}
	email, err := waitVisible(ctx, page, `#sso-email`)
	if err != nil {
		return err
	}
	if err := wander(ctx, page, sensorWait); err != nil {
		return err
	}
	if err := typeInto(page, email, account.Email); err != nil {
		return err
	}
	password, err := waitVisible(ctx, page, `#sso-password`)
	if err != nil {
		return err
	}
	if err := typeInto(page, password, account.Password); err != nil {
		return err
	}
	keepLoggedIn(page)
	if err := page.Press(password, "Enter"); err != nil {
		return fmt.Errorf("emirates: submit sign-in: %w", err)
	}
	return followSubmit(ctx, page, code)
}

// openLogin takes page from the booking page to the sign-in form by its
// sign-in link, unless it is on the sign-in pages already. It returns an
// error when the link cannot be found or followed.
func openLogin(ctx context.Context, page *browser.Page) error {
	if onLoginPage(page) {
		return nil
	}
	link, err := waitVisible(ctx, page, loginLinkSelector)
	if err != nil {
		return err
	}
	href, err := page.Evaluate(`document.querySelector('` +
		link + `').href`)
	if err != nil {
		return fmt.Errorf("emirates: sign-in link: %w", err)
	}
	target, _ := json.Marshal(href)
	if _, err := page.Evaluate(
		"location.href = " + string(target),
	); err != nil {
		return fmt.Errorf("emirates: open sign-in: %w", err)
	}
	deadline := time.Now().Add(formWait)
	for time.Now().Before(deadline) && !onLoginPage(page) {
		if err := sleep(ctx, pollDelay/2); err != nil {
			return err
		}
	}
	return nil
}

// followSubmit follows a submitted sign-in form on page until the site
// leaves the sign-in pages, answering one code step with code. It returns
// errSignInNeeded when a code is asked for and code is nil, an error with
// the site's message when the sign-in is refused, and an error when the
// site does not leave the sign-in pages in time.
func followSubmit(
	ctx context.Context,
	page *browser.Page,
	code CodeFunc,
) error {
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
			return fmt.Errorf("%w: %s", errSignInRefused, msg)
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
	return errors.New("emirates: sign-in did not finish")
}

// codeSelector matches the fields of a verification step that take a
// code the site sent.
const codeSelector = `input[autocomplete="one-time-code"],` +
	`input[name*="otp" i], input[id*="otp" i],` +
	`input[name*="code" i]:not([type="hidden"]):not([id="sso-email"])`

// answerCode asks code for the verification code, types it into field on
// page and submits it. It returns an error when code fails or the field
// cannot be filled in or submitted.
func answerCode(
	ctx context.Context,
	page *browser.Page,
	field string,
	code CodeFunc,
) error {
	c, err := code(ctx)
	if err != nil {
		return fmt.Errorf("emirates: verification code: %w", err)
	}
	if err := typeInto(page, field, strings.TrimSpace(c)); err != nil {
		return err
	}
	if err := page.Press(field, "Enter"); err != nil {
		return fmt.Errorf("emirates: submit code: %w", err)
	}
	return nil
}

// keepLoggedIn ticks the form's keep-me-logged-in box on page when it is
// not ticked, so the session lasts.
func keepLoggedIn(page *browser.Page) {
	_, _ = page.Evaluate(`(() => {` +
		`const b = document.querySelector('input[name="rememberMe"]');` +
		`if (b && !b.checked) b.click(); })()`)
}

// signedOutURL reports whether u is the booking page the results page
// sends a visitor who is not signed in to.
func signedOutURL(u string) bool {
	return strings.Contains(u, signedOutMark)
}

// onAccountsURL reports whether u is on the sign-in pages.
func onAccountsURL(u string) bool {
	return strings.Contains(u, "://"+accountsHost+"/")
}

// onLoginPage reports whether page is on the sign-in pages.
func onLoginPage(page *browser.Page) bool {
	u, err := page.URL()
	return err == nil && onAccountsURL(u)
}

// visibleError returns the text of the error message the sign-in form
// shows, cut short, or "" when it shows none.
func visibleError(page *browser.Page) string {
	v, err := page.Evaluate(`[...document.querySelectorAll(` +
		`'[role="alert"], [class*="error"], [class*="Error"]')]` +
		`.filter(e => e.offsetParent && e.innerText.trim()` +
		` && e.innerText.trim().length < 300)` +
		`.map(e => e.innerText.trim())[0] || ''`)
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	if len(s) > 90 {
		s = s[:90]
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
	v, err := page.Evaluate(`(() => { const e = ` +
		`[...document.querySelectorAll(` + string(sel) + `)]` +
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
	return "", errors.New("emirates: sign-in page has no " +
		strings.SplitN(selector, "[", 2)[0])
}

// typeInto clicks the element sel on page, clears it and types text into
// it key by key. It returns an error when the element cannot be clicked
// or typed into.
func typeInto(page *browser.Page, sel, text string) error {
	if err := page.Click(sel); err != nil {
		return fmt.Errorf("emirates: sign-in form: %w", err)
	}
	_, _ = page.Evaluate(`document.querySelector('` + sel + `').select()`)
	if err := page.Type(sel, text); err != nil {
		return fmt.Errorf("emirates: sign-in form: %w", err)
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
