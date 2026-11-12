package main

import (
	"context"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/mattermost/mattermost-plugin-matrix-bridge/server/mas"
)

// tokenFromMAS asks Matrix Authentication Service for a personal session of
// the person's own Matrix account, so double puppeting needs no pasted token.
// It is only called when the person asks for it from that account itself (a
// direct message to the bridge bot), which is the proof of ownership.
func (p *Plugin) tokenFromMAS(matrixUserID string) (string, error) {
	return masPersonalToken(p.getConfiguration(), matrixUserID)
}

// masPersonalToken is tokenFromMAS for callers that hold a configuration.
func masPersonalToken(cfg *configuration, matrixUserID string) (string, error) {
	if !cfg.masConfigured() {
		return "", errors.New("MAS is not configured")
	}
	localpart, ok := localpartOnServer(cfg.MatrixServerDomain, matrixUserID)
	if !ok {
		return "", errors.New("the account is not on this homeserver")
	}
	client := mas.Shared(cfg.MASURL, cfg.MASAdminClientID, cfg.MASAdminClientSecret)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := client.UserIDByUsername(ctx, localpart)
	if err != nil {
		return "", err
	}
	return client.PersonalSession(ctx, id, "Mattermost bridge")
}

// localpartOnThisServer returns the localpart of a Matrix ID that belongs to
// the homeserver the bridge is attached to.
func (p *Plugin) localpartOnThisServer(matrixUserID string) (string, bool) {
	return localpartOnServer(p.getConfiguration().MatrixServerDomain, matrixUserID)
}

func localpartOnServer(domain, matrixUserID string) (string, bool) {
	id := strings.TrimPrefix(matrixUserID, "@")
	i := strings.Index(id, ":")
	if i <= 0 {
		return "", false
	}
	if domain == "" || !strings.EqualFold(id[i+1:], domain) {
		return "", false
	}
	return id[:i], true
}
