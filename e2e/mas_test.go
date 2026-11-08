package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/net/html"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/e2e/capabilities"
	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/mas"
)

func readBody(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return string(b)
}

// registerAsAppservice registers a user the way the bridge does in mirror
// mode: through the appservice, without logging in.
func registerAsAppservice(t *testing.T, client *http.Client, base, localpart string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"type": "m.login.application_service", "username": localpart, "inhibit_login": true})
	req, err := http.NewRequest(http.MethodPost, base+"/_matrix/client/v3/register", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+asToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	require.NoError(t, err)
	return res.StatusCode, readBody(t, res)
}

// probeSubject signs in to the identity provider as the given person and
// returns the subject (sub) it assigns them, as Mattermost would record it.
func probeSubject(t *testing.T, s *masStack, email string) string {
	t.Helper()
	client := s.Client()
	authURL := s.DexBase + "/auth?" + url.Values{
		"client_id": {"probe"}, "redirect_uri": {"http://probe.invalid/callback"}, "response_type": {"code"},
		"scope": {"openid profile email"}, "state": {"s"},
	}.Encode()
	code := walkDexLogin(t, client, s, authURL, email)

	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://probe.invalid/callback"}}
	req, err := http.NewRequest(http.MethodPost, s.DexBase+"/token", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.SetBasicAuth("probe", "probe-secret")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
	require.NoError(t, err)
	tok := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, tok)
	idToken := gjson.Get(tok, "id_token").Str
	parts := strings.Split(idToken, ".")
	require.Len(t, parts, 3, "an id token")
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	sub := gjson.GetBytes(claims, "sub").Str
	require.NotEmpty(t, sub)
	// Dex's local password connector releases the username as "name" (and an empty preferred_username).
	assert.Equal(t, strings.Split(email, "@")[0], gjson.GetBytes(claims, "name").Str, "claims: %s", claims)
	return sub
}

var loginAction = regexp.MustCompile(`action="([^"]*)"`)

// walkDexLogin follows Dex's sign-in for a person and returns the
// authorization code from the final redirect.
func walkDexLogin(t *testing.T, client *http.Client, s *masStack, startURL, email string) string {
	t.Helper()
	next := startURL
	for i := 0; i < 10; i++ {
		if strings.HasPrefix(next, "http://probe.invalid/callback") {
			u, err := url.Parse(next)
			require.NoError(t, err)
			return u.Query().Get("code")
		}
		res, err := client.Get(next)
		require.NoError(t, err, next)
		loc := res.Header.Get("Location")
		body := readBody(t, res)
		if res.StatusCode >= 300 && res.StatusCode < 400 {
			if strings.HasPrefix(loc, "http://probe.invalid/callback") {
				u, err := url.Parse(loc)
				require.NoError(t, err)
				return u.Query().Get("code")
			}
			base, _ := url.Parse(next)
			ref, err := base.Parse(loc)
			require.NoError(t, err)
			next = ref.String()
			continue
		}
		// A sign-in form.
		m := loginAction.FindStringSubmatch(body)
		require.NotNil(t, m, "expected a login form at %s: %s", next, body)
		base, _ := url.Parse(next)
		action, err := base.Parse(strings.ReplaceAll(m[1], "&amp;", "&"))
		require.NoError(t, err)
		form := url.Values{"login": {email}, "password": {dexUserPass}}
		post, err := client.PostForm(action.String(), form)
		require.NoError(t, err)
		loc = post.Header.Get("Location")
		_ = readBody(t, post)
		require.NotEmpty(t, loc, "dex should redirect after signing in (status %d)", post.StatusCode)
		ref, err := action.Parse(loc)
		require.NoError(t, err)
		next = ref.String()
	}
	t.Fatalf("no authorization code after following the sign-in")
	return ""
}

// TestMASSharedLogin checks shared login against a real Matrix Authentication
// Service, Synapse and OpenID Connect provider, in the shape mirror mode
// needs: accounts the bridge registers through its appservice, then signed
// into through the identity provider.
func TestMASSharedLogin(t *testing.T) {
	stack := startMASStack(t)
	client := stack.Client()
	ctx := context.Background()
	masAdmin := mas.New("http://"+stack.hosts["mas:8080"], masAdminID, masAdminPass)

	t.Run("the stack is up", func(t *testing.T) {
		for _, u := range []string{stack.SynapseBase + "/health", stack.MASBase + "/.well-known/openid-configuration", stack.DexBase + "/.well-known/openid-configuration"} {
			res, err := client.Get(u)
			require.NoError(t, err, u)
			_ = res.Body.Close()
			assert.Equal(t, http.StatusOK, res.StatusCode, u)
		}
	})

	var adaMASID string

	t.Run("the bridge can register a mirror account in Synapse while MAS delegates sign-in", func(t *testing.T) {
		status, body := registerAsAppservice(t, client, stack.SynapseBase, "ada")
		t.Logf("appservice register: %d %s", status, body)
		require.Equal(t, http.StatusOK, status, body)
		assert.Equal(t, "@ada:"+masServerName, gjson.Get(body, "user_id").Str)
	})

	t.Run("MAS can be told about that account", func(t *testing.T) {
		id, created, err := masAdmin.EnsureUser(ctx, "ada", "Ada Admin")
		require.NoError(t, err)
		t.Logf("MAS user %s created=%v", id, created)
		assert.True(t, created, "MAS does not know the account yet, so creating it works despite Synapse already having it")
		adaMASID = id

		again, created, err := masAdmin.EnsureUser(ctx, "ada", "Ada Admin")
		require.NoError(t, err)
		assert.False(t, created)
		assert.Equal(t, id, again)
	})

	t.Run("the account can be linked to the person at the identity provider", func(t *testing.T) {
		require.NotEmpty(t, adaMASID)
		sub := probeSubject(t, stack, "ada@example.com")
		t.Logf("ada's subject at the provider: %s", sub)
		require.NoError(t, masAdmin.LinkUpstream(ctx, adaMASID, masProviderID, sub, "ada@example.com"))
		require.NoError(t, masAdmin.LinkUpstream(ctx, adaMASID, masProviderID, sub, "ada@example.com"), "linking twice is harmless")
	})

	t.Run("signing in through the provider lands on the account the bridge made", func(t *testing.T) {
		loginToken := walkMatrixSSO(t, stack, "ada@example.com")

		// Exchange the token the way a Matrix client does.
		body, _ := json.Marshal(map[string]any{"type": "m.login.token", "token": loginToken})
		res, err := client.Post(stack.MASBase+"/_matrix/client/v3/login", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		login := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, login)
		assert.Equal(t, "@ada:"+masServerName, gjson.Get(login, "user_id").Str, "the person lands on the account the bridge registered")
		capabilities.Verify(t, "mirror.sso_provider", gjson.Get(login, "user_id").Str == "@ada:"+masServerName)
		adaToken := gjson.Get(login, "access_token").Str
		require.NotEmpty(t, adaToken)

		// Synapse accepts that token for the same account.
		req, _ := http.NewRequest(http.MethodGet, stack.SynapseBase+"/_matrix/client/v3/account/whoami", nil)
		req.Header.Set("Authorization", "Bearer "+adaToken)
		res, err = client.Do(req)
		require.NoError(t, err)
		whoami := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, whoami)
		assert.Equal(t, "@ada:"+masServerName, gjson.Get(whoami, "user_id").Str)

		// The person and the bridge now share one account: a room the bridge
		// creates for her through the appservice, and a message from her own
		// client in it.
		room := createRoomAsAppservice(t, client, stack.SynapseBase, "@ada:"+masServerName, "Shared account")
		sendText(t, client, stack.SynapseBase, room, adaToken, "", "typed in Element by Ada")
		sendText(t, client, stack.SynapseBase, room, asToken, "@ada:"+masServerName, "posted by the bridge as Ada")
		msgs := roomMessages(t, client, stack.SynapseBase, room, adaToken)
		assert.Contains(t, msgs, "typed in Element by Ada")
		assert.Contains(t, msgs, "posted by the bridge as Ada")
	})

	t.Run("without the bridge telling MAS about the account, the first sign-in fails", func(t *testing.T) {
		// Bob has an account the bridge registered in Synapse, but nothing
		// told MAS about it: MAS refuses a username Synapse already has.
		status, body := registerAsAppservice(t, client, stack.SynapseBase, "bob")
		require.Equal(t, http.StatusOK, status, body)

		token, failure := trySSO(t, stack, "bob@example.com")
		t.Logf("sign-in without provisioning: token=%q failure=%q", token, failure)
		assert.Empty(t, token, "no login token for an account MAS does not know")
		assert.NotEmpty(t, failure)
	})

	t.Run("a subject that belongs to someone else is not linked", func(t *testing.T) {
		bobMAS, _, err := masAdmin.EnsureUser(ctx, "bob", "Bob")
		require.NoError(t, err)
		adaSub := probeSubject(t, stack, "ada@example.com")
		err = masAdmin.LinkUpstream(ctx, bobMAS, masProviderID, adaSub, "")
		require.Error(t, err, "Ada's identity must not be attachable to Bob's account")
	})

	t.Run("once the bridge has provisioned bob, he can sign in", func(t *testing.T) {
		bobMAS, _, err := masAdmin.EnsureUser(ctx, "bob", "Bob")
		require.NoError(t, err)
		require.NoError(t, masAdmin.LinkUpstream(ctx, bobMAS, masProviderID, probeSubject(t, stack, "bob@example.com"), "bob@example.com"))

		token := walkMatrixSSO(t, stack, "bob@example.com")
		body, _ := json.Marshal(map[string]any{"type": "m.login.token", "token": token})
		res, err := client.Post(stack.MASBase+"/_matrix/client/v3/login", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		login := readBody(t, res)
		require.Equal(t, http.StatusOK, res.StatusCode, login)
		assert.Equal(t, "@bob:"+masServerName, gjson.Get(login, "user_id").Str)
	})
}

// walkMatrixSSO signs a person in to Matrix through MAS and the identity
// provider and returns the login token the client receives at the end.
func walkMatrixSSO(t *testing.T, s *masStack, email string) string {
	t.Helper()
	token, failure := trySSO(t, s, email)
	require.Empty(t, failure, "signing in failed")
	return token
}

// trySSO walks the sign-in and returns either the login token or a description
// of where it stopped, so a test can assert on a failing sign-in.
func trySSO(t *testing.T, s *masStack, email string) (token, failure string) {
	t.Helper()
	client := s.Client()
	const clientCallback = "http://client.invalid/cb"
	next := s.MASBase + "/_matrix/client/v3/login/sso/redirect?" + url.Values{"redirectUrl": {clientCallback}}.Encode()
	var method string = http.MethodGet
	var form url.Values
	for hop := 0; hop < 30; hop++ {
		if strings.HasPrefix(next, clientCallback) {
			u, err := url.Parse(next)
			require.NoError(t, err)
			token := u.Query().Get("loginToken")
			if token == "" {
				return "", "the redirect back to the client carries no login token: " + next
			}
			return token, ""
		}
		var res *http.Response
		var err error
		if method == http.MethodPost {
			res, err = client.PostForm(next, form)
		} else {
			res, err = client.Get(next)
		}
		require.NoError(t, err, next)
		loc := res.Header.Get("Location")
		body := readBody(t, res)
		method, form = http.MethodGet, nil
		if loc != "" {
			base, _ := url.Parse(next)
			ref, err := base.Parse(loc)
			require.NoError(t, err)
			next = ref.String()
			continue
		}
		if res.StatusCode >= 400 {
			return "", fmt.Sprintf("hop %d %s answered %d: %s", hop, next, res.StatusCode, truncateForLog(body))
		}

		doc, err := html.Parse(strings.NewReader(body))
		require.NoError(t, err)
		action, fields, ok := firstForm(doc)
		if !ok {
			return "", fmt.Sprintf("hop %d at %s: no form to continue: %s", hop, next, truncateForLog(visibleText(doc)))
		}
		if _, has := fields["login"]; has {
			fields["login"] = []string{email}
			fields["password"] = []string{dexUserPass}
		}
		base, _ := url.Parse(next)
		target, err := base.Parse(strings.ReplaceAll(action, "&amp;", "&"))
		require.NoError(t, err)
		next, method, form = target.String(), http.MethodPost, url.Values(fields)
		t.Logf("hop %d: submitting the form at %s", hop, next)
	}
	return "", "signing in did not finish within 30 steps"
}

// visibleText is the text of a page without markup, for failure messages.
func visibleText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode {
			if t := strings.TrimSpace(n.Data); t != "" {
				sb.WriteString(t + " ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

func truncateForLog(s string) string {
	if len(s) > 1200 {
		return s[:1200]
	}
	return s
}

// firstForm returns the action and the fields (hidden inputs, filled inputs and
// the first submit button) of the first form in the page.
func firstForm(n *html.Node) (action string, fields map[string][]string, ok bool) {
	var form *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		if form != nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "form" {
			form = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(n)
	if form == nil {
		return "", nil, false
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	fields = map[string][]string{}
	submitted := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "input" || n.Data == "button") {
			name, typ := attr(n, "name"), attr(n, "type")
			switch {
			case name == "":
			case n.Data == "button" || typ == "submit":
				if !submitted {
					fields[name] = []string{attr(n, "value")}
					submitted = true
				}
			default:
				fields[name] = []string{attr(n, "value")}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(form)
	return attr(form, "action"), fields, true
}

func createRoomAsAppservice(t *testing.T, client *http.Client, base, asUser, name string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": name, "preset": "private_chat"})
	req, err := http.NewRequest(http.MethodPost, base+"/_matrix/client/v3/createRoom?user_id="+url.QueryEscape(asUser), bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+asToken)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	require.NoError(t, err)
	out := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, out)
	return gjson.Get(out, "room_id").Str
}

var txnCounter int

// sendText sends a message with the given token; asUser impersonates a user
// (appservice only).
func sendText(t *testing.T, client *http.Client, base, room, token, asUser, text string) {
	t.Helper()
	txnCounter++
	u := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/send/m.room.message/%d", base, url.PathEscape(room), txnCounter)
	if asUser != "" {
		u += "?user_id=" + url.QueryEscape(asUser)
	}
	body, _ := json.Marshal(map[string]any{"msgtype": "m.text", "body": text})
	req, err := http.NewRequest(http.MethodPut, u, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	require.NoError(t, err)
	out := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, out)
}

func roomMessages(t *testing.T, client *http.Client, base, room, token string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/messages?dir=b&limit=20", base, url.PathEscape(room)), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := client.Do(req)
	require.NoError(t, err)
	out := readBody(t, res)
	require.Equal(t, http.StatusOK, res.StatusCode, out)
	var bodies []string
	gjson.Get(out, "chunk").ForEach(func(_, ev gjson.Result) bool {
		if ev.Get("type").Str == "m.room.message" {
			assert.Equal(t, "@ada:"+masServerName, ev.Get("sender").Str, "both messages come from the one account")
			bodies = append(bodies, ev.Get("content.body").Str)
		}
		return true
	})
	return bodies
}
