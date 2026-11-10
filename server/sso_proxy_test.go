package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDropEmptyTokenFields(t *testing.T) {
	out := dropEmptyTokenFields([]byte(`{"access_token":"a","token_type":"bearer","scope":"","id_token":"","expires_in":10}`))
	assert.JSONEq(t, `{"access_token":"a","token_type":"bearer","expires_in":10}`, string(out))

	kept := dropEmptyTokenFields([]byte(`{"access_token":"a","scope":"user","id_token":"x.y.z"}`))
	assert.JSONEq(t, `{"access_token":"a","scope":"user","id_token":"x.y.z"}`, string(kept))

	assert.Equal(t, "not json", string(dropEmptyTokenFields([]byte("not json"))))
}

func TestSSOTokenPassesTheExchangeToMattermost(t *testing.T) {
	var gotForm, gotAuth string
	mm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/oauth/access_token", r.URL.Path)
		_ = r.ParseForm()
		gotForm = r.PostForm.Get("code")
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"bearer","scope":"","id_token":""}`))
	}))
	defer mm.Close()

	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	p.configuration.MASURL = "https://mas.example.com"
	cfg := &model.Config{}
	cfg.SetDefaults()
	cfg.ServiceSettings.SiteURL = model.NewPointer(mm.URL)
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetConfig")
	api.On("GetConfig").Return(cfg)

	req := httptest.NewRequest(http.MethodPost, ssoTokenPath, strings.NewReader("grant_type=authorization_code&code=abc"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic xyz")
	rec := httptest.NewRecorder()
	p.ServeHTTP(nil, rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "abc", gotForm)
	assert.Equal(t, "Basic xyz", gotAuth)
	assert.JSONEq(t, `{"access_token":"tok","token_type":"bearer"}`, rec.Body.String())
}

func TestSSOTokenOnlyAnswersPost(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	rec := httptest.NewRecorder()
	p.ServeHTTP(nil, rec, httptest.NewRequest(http.MethodGet, ssoTokenPath, nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestSSOTokenIsOffUnlessMASIsConfigured(t *testing.T) {
	hs := newFakeSynapse(t)
	p, _ := newMirrorTestBridge(t, hs)
	rec := httptest.NewRecorder()
	p.ServeHTTP(nil, rec, httptest.NewRequest(http.MethodPost, ssoTokenPath, strings.NewReader("x=y")))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSSOTokenIsRateLimitedPerAddress(t *testing.T) {
	limiter := newAddressLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		assert.True(t, limiter.Allow("203.0.113.5"))
	}
	assert.False(t, limiter.Allow("203.0.113.5"), "the fourth request in the window is refused")
	assert.True(t, limiter.Allow("203.0.113.6"), "another address is unaffected")

	short := newAddressLimiter(1, 10*time.Millisecond)
	assert.True(t, short.Allow("a"))
	assert.False(t, short.Allow("a"))
	time.Sleep(15 * time.Millisecond)
	assert.True(t, short.Allow("a"), "the window passes")
}

func TestClientAddressPrefersTheForwardedHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "10.0.0.1:5555"
	assert.Equal(t, "10.0.0.1", clientAddress(r))
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	assert.Equal(t, "198.51.100.7", clientAddress(r))
}
