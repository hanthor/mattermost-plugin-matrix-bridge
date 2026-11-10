package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/store/kvstore"
)

func TestWidgetPage(t *testing.T) {
	hs := newFakeSynapse(t)
	p, api := newMirrorTestBridge(t, hs)
	site := "https://chat.example.com"
	api.ExpectedCalls = filterCalls(api.ExpectedCalls, "GetConfig")
	api.On("GetConfig").Return(&model.Config{ServiceSettings: model.ServiceSettings{SiteURL: &site}}).Maybe()
	api.On("GetChannel", "ch1").Return(&model.Channel{Id: "ch1", TeamId: "t1", Name: "town-square", DisplayName: "Town <Square>", Purpose: "Where it happens", Type: model.ChannelTypeOpen}, nil).Maybe()
	api.On("GetTeam", "t1").Return(&model.Team{Id: "t1", Name: "main", DisplayName: "Main"}, nil).Maybe()
	api.On("GetChannelStats", "ch1").Return(&model.ChannelStats{MemberCount: 4}, nil).Maybe()
	api.On("GetChannel", mock.Anything).Return(nil, &model.AppError{Message: "none"}).Maybe()
	_ = p.kvstore.Set(kvstore.BuildChannelMappingKey("ch1"), []byte("!room:example.com"))

	get := func(channel, sig string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		p.handleWidget(rec, httptest.NewRequest(http.MethodGet, "/widget?channel="+channel+"&sig="+sig, nil))
		return rec
	}

	t.Run("a signed address shows the channel, escaped", func(t *testing.T) {
		rec := get("ch1", p.widgetSignature("ch1"))
		assert.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, "Town &lt;Square&gt;")
		assert.Contains(t, body, "4 members")
		assert.Contains(t, body, "https://chat.example.com/main/channels/town-square")
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors *")
	})
	t.Run("a wrong or missing signature shows nothing", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, get("ch1", "nope").Code)
		assert.Equal(t, http.StatusNotFound, get("ch1", "").Code)
		assert.Equal(t, http.StatusNotFound, get("ch2", p.widgetSignature("ch1")).Code)
	})
	t.Run("a channel that is not bridged shows nothing", func(t *testing.T) {
		_ = p.kvstore.Delete(kvstore.BuildChannelMappingKey("ch1"))
		assert.Equal(t, http.StatusNotFound, get("ch1", p.widgetSignature("ch1")).Code)
	})
}
